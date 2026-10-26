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
	"slices"
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

In a terminal, sweep opens a list of what it found, biggest first:
  ↑/↓ or j/k    move
  space         delete the highlighted folder right away
  q             quit

Flags:
  -n, --dry-run   only print the list, delete nothing
  -y, --yes       delete everything listed, without opening the list
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
	isTTY          bool                               // stdin is a terminal
	color          bool                               // stdout takes ANSI colors
	cols           int                                // stdout's width; 0 when it isn't a terminal
	raw            func() (restore func(), err error) // raw terminal input for the interactive screen; nil in tests
	size           func() (cols, rows int)            // terminal size for the interactive screen
	live           bool                               // stderr is a terminal: show scan progress
	flushInput     func()                             // discards type-ahead before the prompt; nil in tests
	stdout, stderr io.Writer
	workers        int
}

func main() {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version // go install ...@v1.2.3
	}
	cols := 0
	if isTerminal(os.Stdout) {
		cols = termWidth(os.Stdout)
	}
	os.Exit(run(os.Args[1:], env{
		configPath: configPath(), cols: cols,
		raw:    func() (func(), error) { return makeRaw(os.Stdin) },
		size:   func() (int, int) { return termSize(os.Stdout) },
		asRoot: os.Geteuid() == 0, homes: sweep.Homes(), system: sweep.SystemRoots,
		stdin: os.Stdin, isTTY: isTerminal(os.Stdin), flushInput: flushTypeahead,
		color:  colorOK(isTerminal(os.Stdout), os.Getenv),
		live:   isTerminal(os.Stderr) && os.Getenv("TERM") != "dumb",
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
	var progress sweep.Progress
	stop := watch(e, &progress, root)
	cands, kept := sweep.Scan(root, targets, e.workers, &progress)
	stop()
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
	// In a terminal, deleting is interactive: pick folders and delete them
	// one by one. -n only lists; -y deletes everything without asking.
	if !dryRun && !yes && e.isTTY && e.cols > 0 {
		return interactive(e, r, targets, root, cands, len(kept))
	}

	// Paths on the left, sizes right-aligned in one column after the longest
	// path, kept within the terminal. A longer path isn't cut (it's a list of
	// what gets deleted); it just pushes its own size along.
	//
	// In a long list, folders under 1 MB are folded into one line: they're
	// most of the rows but hardly any of the space. They're still deleted.
	shown := len(cands)
	if len(cands) > 20 {
		shown = slices.IndexFunc(cands, func(c sweep.Candidate) bool { return c.Size < 1<<20 })
		if shown < 0 {
			shown = len(cands)
		}
	}
	paths := make([]string, shown)
	var total, folded int64
	for i, c := range cands {
		total += c.Size
		if i >= shown {
			folded += c.Size
			continue
		}
		rel, _ := filepath.Rel(root, c.Path)
		paths[i] = printable(rel)
	}
	foldLabel := "+ " + strings.Replace(folders(len(cands)-shown), "folder", "small folder", 1) + " (under 1 MB each)"
	col := len(folders(len(cands)))
	for _, l := range paths {
		col = max(col, utf8.RuneCountInString(l))
	}
	if shown < len(cands) {
		col = max(col, utf8.RuneCountInString(foldLabel))
	}
	width := e.cols
	if width == 0 {
		width = 80
	}
	col = max(min(col, width-13), 1) // 2 spaces + 10 for the size + 1 spare
	row := func(label, labelStyle string, size int64, style string) {
		pad := strings.Repeat(" ", max(col-utf8.RuneCountInString(label), 0))
		fmt.Fprintf(e.stdout, "%s%s  %s\n", e.paint(labelStyle, label), pad, e.paint(style, fmt.Sprintf("%10s", human(size))))
	}
	for i, p := range paths {
		row(p, "", cands[i].Size, sizeStyle(cands[i].Size))
	}
	if shown < len(cands) {
		row(foldLabel, dim, folded, dim)
	}
	fmt.Fprintln(e.stdout, strings.Repeat("-", col+12))
	row(folders(len(cands)), bold, total, bold)
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
	fmt.Fprintln(e.stdout, e.paint(green, fmt.Sprintf("Deleted %s, freed about %s.", folders(len(deleted)), human(freed))))
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
