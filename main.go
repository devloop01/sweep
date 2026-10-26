//go:build darwin && arm64

// sweep finds and deletes node_modules and framework build/cache folders
// (.svelte-kit, .turbo, .next, ...) under a directory.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/devloop01/sweep/internal/sweep"
)

var version = "dev" // set with -ldflags "-X main.version=..."

const usage = `sweep — find and delete node_modules and framework build/cache folders
(.svelte-kit, .turbo, .next, ...) under a directory.

Usage:
  sweep [flags] [DIR]    DIR defaults to the current directory

Flags:
  -n, --dry-run   only list what would be deleted
  -y, --yes       delete without asking
  -h, --help      show this help
      --version   print the version

Everything sweep deletes by default comes back with npm/pnpm install or the
next dev/build run.

To add or remove folder names, list them in ~/.config/sweep/targets.conf,
one per line (!name stops deleting one). See
https://github.com/devloop01/sweep#customize
`

// env is everything run needs from the outside world, so tests can fake it.
type env struct {
	configPath     string // user's targets.conf; "" for built-in lists only
	asRoot         bool
	homes          []string // every path naming the home folder
	system         []string // refused system roots
	stdin          io.Reader
	isTTY          bool   // stdin is a terminal
	flushInput     func() // discards type-ahead before the prompt; nil in tests
	stdout, stderr io.Writer
	workers        int
}

func main() {
	fi, err := os.Stdin.Stat()
	isTTY := err == nil && fi.Mode()&os.ModeCharDevice != 0
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version // go install ...@v1.2.3
	}
	os.Exit(run(os.Args[1:], env{
		configPath: configPath(),
		asRoot:     os.Geteuid() == 0, homes: sweep.Homes(), system: sweep.SystemRoots,
		stdin: os.Stdin, isTTY: isTTY, flushInput: flushTypeahead,
		stdout: os.Stdout, stderr: os.Stderr,
		workers: runtime.NumCPU(),
	}))
}

// configPath is $XDG_CONFIG_HOME/sweep/targets.conf, or
// ~/.config/sweep/targets.conf when that isn't set.
func configPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "sweep", "targets.conf")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "sweep", "targets.conf")
}

// flushTypeahead discards anything typed while scanning, so a "y" and
// Enter pressed early can't answer the prompt before the list is shown.
func flushTypeahead() {
	fread := int32(1) // FREAD from <sys/fcntl.h>: flush input only
	syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCFLUSH, uintptr(unsafe.Pointer(&fread)))
}

func run(args []string, e env) int {
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() {}
	var dryRun, yes, showVersion bool
	for _, f := range []struct {
		p           *bool
		short, long string
	}{{&dryRun, "n", "dry-run"}, {&yes, "y", "yes"}} {
		fs.BoolVar(f.p, f.short, false, "")
		fs.BoolVar(f.p, f.long, false, "")
	}
	fs.BoolVar(&showVersion, "version", false, "")

	// flag stops at the first positional, so take it and keep parsing to
	// allow flags after DIR. Everything after "--" is positional.
	var dirs []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(e.stdout, usage)
				return 0
			}
			fmt.Fprint(e.stderr, usage)
			return 2
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			dirs = append(dirs, rest...)
			break
		}
		dirs, args = append(dirs, rest[0]), rest[1:]
	}
	if showVersion {
		fmt.Fprintln(e.stdout, version)
		return 0
	}
	if len(dirs) > 1 {
		fmt.Fprintf(e.stderr, "sweep: expected at most one DIR, got %d\n%s", len(dirs), usage)
		return 2
	}
	dir := "."
	if len(dirs) == 1 {
		dir = dirs[0]
	}
	if e.asRoot {
		// Under sudo both $HOME and the user record can point at root's
		// home, so the home-folder guard can't be trusted.
		fmt.Fprintln(e.stderr, "sweep: refusing to run as root — run it as yourself, without sudo.")
		return 1
	}

	root, err := sweep.Resolve(dir)
	if err == nil {
		err = sweep.CheckRoot(root, e.homes, e.system)
	}
	var r *os.Root
	if err == nil {
		r, err = sweep.Open(root) // before scanning: deletion stays in this folder
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "sweep:", printable(err.Error()))
		return 1
	}
	defer r.Close()

	targets, custom := sweep.DefaultTargets(), false
	if e.configPath != "" {
		if targets, custom, err = sweep.LoadTargets(e.configPath); err != nil {
			fmt.Fprintln(e.stderr, "sweep:", printable(err.Error()))
			return 1
		}
	}
	if custom {
		fmt.Fprintf(e.stdout, "Using targets from %s\n", printable(e.configPath))
	}
	fmt.Fprintf(e.stdout, "Scanning %s ...\n", printable(root))
	cands, kept := sweep.Scan(root, targets, e.workers)
	// Kept folders aren't listed, but a dist/ that survives shouldn't be a mystery.
	skipped := func() {
		if len(kept) > 0 {
			fmt.Fprintf(e.stdout, "Skipped %s that may hold real files: build folders outside git, or ones git tracks files in.\n", folders(len(kept)))
		}
	}
	if len(cands) == 0 {
		fmt.Fprintln(e.stdout, "Nothing to clean.")
		skipped()
		return 0
	}
	var total int64
	for _, c := range cands {
		rel, _ := filepath.Rel(root, c.Path)
		fmt.Fprintf(e.stdout, "%10s  %s\n", human(c.Size), printable(rel))
		total += c.Size
	}
	fmt.Fprintf(e.stdout, "----\n%s, %s\n", folders(len(cands)), human(total))
	skipped()

	if dryRun {
		fmt.Fprintln(e.stdout, "Dry run: nothing deleted.")
		return 0
	}
	if !yes && !confirm(e) {
		fmt.Fprintln(e.stdout, "Cancelled.")
		return 0
	}

	deleted, errs := sweep.Remove(r, targets, cands, e.workers)
	var freed int64
	for _, c := range deleted {
		freed += c.Size
	}
	for _, err := range errs {
		fmt.Fprintln(e.stderr, "sweep:", printable(err.Error()))
	}
	if len(errs) > 0 {
		fmt.Fprintf(e.stdout, "Deleted %d of %s, freed about %s.\n", len(deleted), folders(len(cands)), human(freed))
		return 1
	}
	fmt.Fprintf(e.stdout, "Deleted %s, freed about %s.\n", folders(len(deleted)), human(freed))
	return 0
}

// confirm asks once. Only y/yes proceeds; EOF or a non-terminal stdin cancels.
func confirm(e env) bool {
	fmt.Fprint(e.stdout, "Delete all of these? [y/N] ")
	if !e.isTTY {
		fmt.Fprintln(e.stdout)
		fmt.Fprintln(e.stderr, "sweep: stdin is not a terminal; use --yes to delete without asking.")
		return false
	}
	if e.flushInput != nil {
		e.flushInput()
	}
	line, err := bufio.NewReader(e.stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(e.stdout)
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// folders returns "1 folder" or "n folders".
func folders(n int) string {
	if n == 1 {
		return "1 folder"
	}
	return fmt.Sprintf("%d folders", n)
}

// human formats bytes with base 1024 and one decimal.
func human(b int64) string {
	const k = 1024.0
	switch f := float64(b); {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f GB", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f MB", f/(k*k))
	default:
		return fmt.Sprintf("%.1f KB", f/k)
	}
}

// printable replaces control and format characters (newlines, bidi
// overrides, zero-width), line/paragraph separators and invalid UTF-8 with
// '?', so a folder name can't spoof the list.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return '?'
		}
		return r
	}, s)
}
