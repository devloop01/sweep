//go:build darwin && arm64

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// fixture makes a temp DIR with two targets and a file that must survive.
func fixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a/node_modules/pkg", "c/.svelte-kit", "e/weird\nname/node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "a/node_modules/pkg/f"), make([]byte, 10_000), 0o644)
	os.WriteFile(filepath.Join(root, "a/keep.txt"), nil, 0o644)
	return root
}

// sweepIn runs the CLI with a fake home (never the real one) and no system
// roots, because t.TempDir lives under /private on macOS.
func sweepIn(t *testing.T, stdin string, tty bool, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, env{
		homes: []string{t.TempDir()}, stdin: strings.NewReader(stdin), isTTY: tty,
		stdout: &o, stderr: &e, workers: 4,
	})
	return code, o.String(), e.String()
}

func targetsLeft(root string) int {
	n := 0
	for _, d := range []string{"a/node_modules", "c/.svelte-kit", "e/weird\nname/node_modules"} {
		if _, err := os.Lstat(filepath.Join(root, d)); err == nil {
			n++
		}
	}
	return n
}

func TestPromptDeclined(t *testing.T) {
	for name, tc := range map[string]struct {
		stdin string
		tty   bool
	}{
		"n":               {"n\n", true},
		"empty":           {"\n", true},
		"EOF":             {"", true},
		"y without enter": {"y", true},
		"yeah":            {"yeah\n", true},
		"y but not a tty": {"y\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			code, out, _ := sweepIn(t, tc.stdin, tc.tty, root)
			if code != 0 || !strings.Contains(out, "Move all of these to the Trash? [y/N] ") || !strings.Contains(out, "Cancelled.") {
				t.Errorf("code %d, out:\n%s", code, out)
			}
			if n := targetsLeft(root); n != 3 {
				t.Errorf("%d of 3 targets left", n)
			}
		})
	}
}

func TestPromptAccepted(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", " YES \n"} {
		root := fixture(t)
		code, out, _ := sweepIn(t, answer, true, root)
		if code != 0 || !strings.Contains(out, "Moved 3 folders to the Trash, about") {
			t.Errorf("%q: code %d, out:\n%s", answer, code, out)
		}
		if n := targetsLeft(root); n != 0 {
			t.Errorf("%q: %d targets left", answer, n)
		}
		if _, err := os.Stat(filepath.Join(root, "a/keep.txt")); err != nil {
			t.Errorf("%q: keep.txt deleted", answer)
		}
	}
}

func TestYesSkipsPrompt(t *testing.T) {
	root := fixture(t)
	code, out, _ := sweepIn(t, "", false, "--yes", root)
	if code != 0 || strings.Contains(out, "[y/N]") || targetsLeft(root) != 0 {
		t.Errorf("code %d, left %d, out:\n%s", code, targetsLeft(root), out)
	}
}

// An empty DIR (a script's unset variable) is an error, not the current
// folder.
func TestEmptyDirIsRefused(t *testing.T) {
	root := fixture(t)
	t.Chdir(root)
	code, out, errOut := sweepIn(t, "", false, "--yes", "")
	if code != 1 || !strings.Contains(errOut, "DIR is empty") || targetsLeft(root) != 3 {
		t.Errorf("code %d, left %d, out:\n%s\nerr:\n%s", code, targetsLeft(root), out, errOut)
	}
}

func TestDryRun(t *testing.T) {
	root := fixture(t)
	code, out, _ := sweepIn(t, "y\n", true, root, "-n") // flag after DIR
	if code != 0 || targetsLeft(root) != 3 {
		t.Fatalf("code %d, left %d", code, targetsLeft(root))
	}
	// Sizes depend on the filesystem's block size, so match on shape: paths
	// left, then when each changed, then sizes right-aligned, and the total
	// with the disk's free space under them.
	want := regexp.MustCompile(`^Scanning ` + regexp.QuoteMeta(root) + ` \.\.\.
a/node_modules +today +\d+\.\d KB
c/\.svelte-kit +today +\d+\.\d KB
e/weird\?name/node_modules +today +\d+\.\d KB
-+
3 folders · \d+\.\d [KMGT]B free +\d+\.\d KB
Dry run: nothing moved\.
$`)
	if !want.MatchString(out) {
		t.Errorf("out:\n%s", out)
	}
	// Every row lines up: path column + 2 + 8 for the age + 2 + 10.
	lines := strings.Split(out, "\n")[1:6]
	for _, l := range lines {
		if n, w := utf8.RuneCountInString(l), utf8.RuneCountInString(lines[0]); n != w || n < 25+22 {
			t.Errorf("row %d wide, want %d (and at least 47): %q", n, w, l)
		}
	}
}

func TestNothingToClean(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	code, out, _ := sweepIn(t, "", true, root)
	if code != 0 || !strings.HasSuffix(out, "Nothing to clean.\n") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
}

func TestRefusedRoot(t *testing.T) {
	root := fixture(t)
	var o, e bytes.Buffer
	code := run([]string{"--yes", root}, env{homes: []string{root}, stdout: &o, stderr: &e, workers: 4})
	if code != 1 || !strings.Contains(e.String(), "refusing to run") || targetsLeft(root) != 3 {
		t.Errorf("code %d, left %d, stderr: %s", code, targetsLeft(root), e.String())
	}
	o.Reset()
	e.Reset()
	code = run([]string{"--yes", root}, env{homes: []string{t.TempDir()}, system: []string{filepath.Dir(root)}, stdout: &o, stderr: &e, workers: 4})
	if code != 1 || targetsLeft(root) != 3 {
		t.Errorf("system root: code %d, left %d", code, targetsLeft(root))
	}
}

func TestTrashFailure(t *testing.T) {
	root := fixture(t)
	a := filepath.Join(root, "a") // a/node_modules can't leave a
	os.Chmod(a, 0o500)
	t.Cleanup(func() { os.Chmod(a, 0o755) })
	code, out, errOut := sweepIn(t, "", false, "-y", root)
	if code != 1 || !strings.Contains(out, "Moved 2 of 3 folders to the Trash, about ") || !strings.Contains(errOut, "permission denied") {
		t.Errorf("code %d, out:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(root, "a/node_modules/pkg/f")); err != nil {
		t.Error("lost a file of the folder that couldn't be moved")
	}
}

// useTrash gives the test its own empty Trash and returns it.
func useTrash(t *testing.T) string {
	t.Helper()
	trash, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	was := findTrash
	findTrash = func(string) (string, error) { return trash, nil }
	t.Cleanup(func() { findTrash = was })
	return trash
}

// TestMain points the Trash at a temporary folder, so no test ever puts
// anything in the real one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sweep-trash")
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	findTrash = func(string) (string, error) { return dir, nil }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Folders go to the Trash, whole, under names that say where they came
// from.
func TestTrash(t *testing.T) {
	trash := useTrash(t)
	root := fixture(t)
	code, out, _ := sweepIn(t, "", false, "-y", root)
	if code != 0 || targetsLeft(root) != 0 || !strings.Contains(out, "Moved 3 folders to the Trash, about") || !strings.Contains(out, emptyHint) {
		t.Fatalf("code %d, left %d, out:\n%s", code, targetsLeft(root), out)
	}
	var names []string
	entries, _ := os.ReadDir(trash)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	top := filepath.Base(root)
	if want := []string{top + " › a › node_modules", top + " › c › .svelte-kit", top + " › e › weird\nname › node_modules"}; !slices.Equal(names, want) {
		t.Errorf("Trash holds %q, want %q", names, want)
	}
	if _, err := os.Stat(filepath.Join(trash, top+" › a › node_modules/pkg/f")); err != nil {
		t.Error("a moved folder lost its files")
	}
}

// Without a usable Trash sweep stops before scanning, unless it won't need
// one: a dry run.
func TestNoTrash(t *testing.T) {
	was := findTrash
	findTrash = func(string) (string, error) { return "", errors.New("no Trash on this disk") }
	t.Cleanup(func() { findTrash = was })
	root := fixture(t)
	code, out, errOut := sweepIn(t, "y\n", true, root)
	if code != 1 || targetsLeft(root) != 3 || strings.Contains(out, "Scanning") || !strings.Contains(errOut, "no Trash on this disk") {
		t.Errorf("code %d, left %d, out:\n%s\nstderr:\n%s", code, targetsLeft(root), out, errOut)
	}
	if code, _, _ := sweepIn(t, "", false, "-n", root); code != 0 {
		t.Errorf("dry run: code %d", code)
	}
}

func TestArgs(t *testing.T) {
	root := fixture(t)
	for _, tc := range []struct {
		args []string
		code int
		out  string
	}{
		{[]string{root, root}, 2, ""},
		{[]string{"--bogus", root}, 2, ""},
		{[]string{root, "-x"}, 2, ""},
		{[]string{"-h"}, 0, "Usage:"},
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"--version"}, 0, "dev"},
		{[]string{filepath.Join(root, "missing")}, 1, ""},
		{[]string{filepath.Join(root, "a/keep.txt")}, 1, ""},
		{[]string{"--dry-run", "--", root}, 0, "Dry run"},
		{[]string{"--", root, "-n"}, 2, ""}, // after --, -n is a second DIR
		{[]string{"-n", "-b", root}, 2, ""}, // -b/-d were removed: folders go in the config now
		{[]string{"--build", root}, 2, ""},
		{[]string{"--deploy", root}, 2, ""},
	} {
		code, out, _ := sweepIn(t, "", false, tc.args...)
		if code != tc.code || !strings.Contains(out, tc.out) {
			t.Errorf("%q: code %d, want %d; out:\n%s", tc.args, code, tc.code, out)
		}
	}
	if targetsLeft(root) != 3 {
		t.Error("args tests deleted something")
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"weird\nname":   "weird?name",
		"a\tb\x1b[2J":   "a?b?[2J",
		"evil\u202etxt": "evil?txt",
		"zero\u200bw":   "zero?w",
		"line\u2028sep": "line?sep",
		"para\u2029sep": "para?sep",
		"bad\xffutf8":   "bad?utf8",
		"ünï -x y":      "ünï -x y",
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHuman(t *testing.T) {
	for b, want := range map[int64]string{
		0: "0.0 KB", 12_000: "12.0 KB", 340_500_000: "340.5 MB",
		1_200_000_000: "1.2 GB", 3_000_000_000_000: "3.0 TB", 1 << 30: "1.1 GB",
	} {
		if got := human(b); got != want {
			t.Errorf("human(%d) = %q, want %q", b, got, want)
		}
	}
}

func TestRefusesRoot(t *testing.T) {
	root := fixture(t)
	var o, e bytes.Buffer
	code := run([]string{"--yes", root}, env{asRoot: true, homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4})
	if code != 1 || !strings.Contains(e.String(), "refusing to run as root") || targetsLeft(root) != 3 {
		t.Errorf("code %d, left %d, stderr: %s", code, targetsLeft(root), e.String())
	}
}

// flushReader records whether input was flushed before the first read.
type flushReader struct {
	flushed, flushedFirst bool
	r                     io.Reader
}

func (f *flushReader) Read(p []byte) (int, error) {
	f.flushedFirst = f.flushedFirst || f.flushed
	return f.r.Read(p)
}

func TestPromptFlushesTypeahead(t *testing.T) {
	root := fixture(t)
	in := &flushReader{r: strings.NewReader("n\n")}
	var o, e bytes.Buffer
	run([]string{root}, env{
		homes: []string{t.TempDir()}, stdin: in, isTTY: true,
		flushInput: func() { in.flushed = true }, stdout: &o, stderr: &e, workers: 4,
	})
	if !in.flushedFirst {
		t.Error("prompt read input without flushing type-ahead first")
	}
}

// writeConfig writes a user targets.conf and returns its path.
func writeConfig(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "targets.conf")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUserConfig(t *testing.T) {
	root := fixture(t)
	os.MkdirAll(filepath.Join(root, "a/.cache/x"), 0o755)
	var o, e bytes.Buffer
	// Adds .cache to the built-in list and takes .svelte-kit out of it.
	cfg := writeConfig(t, ".cache\n!.svelte-kit\n")
	code := run([]string{"--yes", root}, env{
		configPath: cfg, homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4,
	})
	if code != 0 || !strings.Contains(o.String(), "Using targets from "+cfg) || !strings.Contains(o.String(), "Moved 3 folders to the Trash, ") {
		t.Errorf("code %d, out:\n%s\nstderr:\n%s", code, o.String(), e.String())
	}
	if _, err := os.Stat(filepath.Join(root, "a/.cache")); err == nil {
		t.Error("a/.cache wasn't deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "c/.svelte-kit")); err != nil {
		t.Error("c/.svelte-kit was deleted although the config removed it")
	}
	if targetsLeft(root) != 1 { // only c/.svelte-kit
		t.Errorf("%d targets left, want 1", targetsLeft(root))
	}
}

func TestBadUserConfigDeletesNothing(t *testing.T) {
	root := fixture(t)
	var o, e bytes.Buffer
	cfg := writeConfig(t, "node_modules\n.cache\nsrc/lib/x\n")
	code := run([]string{"--yes", root}, env{
		configPath: cfg, homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4,
	})
	if code != 1 || !strings.Contains(e.String(), cfg+" line 3") || strings.Contains(o.String(), "Scanning") {
		t.Errorf("code %d, out:\n%s\nstderr:\n%s", code, o.String(), e.String())
	}
	if targetsLeft(root) != 3 {
		t.Error("deleted something despite a bad config")
	}
}

func TestNoUserConfigUsesDefaults(t *testing.T) {
	root := fixture(t)
	var o, e bytes.Buffer
	code := run([]string{"-n", root}, env{
		configPath: filepath.Join(t.TempDir(), "missing.conf"), homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4,
	})
	if code != 0 || strings.Contains(o.String(), "Using targets") || !strings.Contains(o.String(), "3 folders") {
		t.Errorf("code %d, out:\n%s", code, o.String())
	}
}

func TestConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if got := configPath(); got != "/x/config/sweep/targets.conf" {
		t.Errorf("with XDG_CONFIG_HOME: %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative") // ignored, like the XDG spec says
	t.Setenv("HOME", "/Users/me")
	if got := configPath(); got != "/Users/me/.config/sweep/targets.conf" {
		t.Errorf("without XDG_CONFIG_HOME: %q", got)
	}
}

func TestFolders(t *testing.T) {
	for n, want := range map[int]string{0: "0 folders", 1: "1 folder", 2: "2 folders", 68: "68 folders"} {
		if got := folders(n); got != want {
			t.Errorf("folders(%d) = %q, want %q", n, got, want)
		}
	}
}

// Folders kept because of git aren't listed, but their count is.
func TestKeptFoldersAreReported(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := fixture(t)
	os.MkdirAll(filepath.Join(root, "p/dist"), 0o755)
	os.WriteFile(filepath.Join(root, "p/dist/app.js"), nil, 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "dist/app.js"}} {
		if out, err := exec.Command("git", append([]string{"-C", filepath.Join(root, "p")}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	code, out, _ := sweepIn(t, "", false, "-y", root)
	if code != 0 || !strings.Contains(out, "Skipped 1 folder that may hold your files") || strings.Contains(out, "p/dist") || !strings.Contains(out, "Moved 3 folders to the Trash") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "p/dist/app.js")); err != nil {
		t.Error("deleted a tracked folder")
	}
}

// In a narrow terminal the size column moves in, and a path too long for it
// is printed whole (never cut) with its size after it.
func TestLayoutNarrowTerminal(t *testing.T) {
	root := fixture(t)
	var o, e bytes.Buffer
	run([]string{"-n", root}, env{cols: 32, homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4})
	lines := strings.Split(o.String(), "\n")
	byPath := map[string]string{}
	for _, l := range lines[1:4] {
		byPath[strings.Fields(l)[0]] = l
	}
	if l := byPath["a/node_modules"]; utf8.RuneCountInString(l) != 31 {
		t.Errorf("short path row should be 32-1 wide: %q", l)
	}
	if l := byPath["e/weird?name/node_modules"]; !strings.HasPrefix(l, "e/weird?name/node_modules  ") {
		t.Errorf("long path cut or misplaced: %q", l)
	}
}

// In a long list, folders under 1 MB fold into one line, but they're still
// counted and deleted.
func TestSmallFoldersFoldInLongLists(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for i := range 22 {
		os.MkdirAll(filepath.Join(root, fmt.Sprintf("p%02d/.turbo", i)), 0o755)
	}
	os.MkdirAll(filepath.Join(root, "big/node_modules"), 0o755)
	os.WriteFile(filepath.Join(root, "big/node_modules/f"), make([]byte, 2<<20), 0o644)

	code, out, _ := sweepIn(t, "", false, "-y", root)
	if code != 0 || !strings.Contains(out, "big/node_modules") || strings.Contains(out, "p00/.turbo") ||
		!strings.Contains(out, "+ 22 small folders (under 1 MB each)") || !strings.Contains(out, "23 folders · ") ||
		!strings.Contains(out, "Moved 23 folders to the Trash") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "p00")); len(entries) != 0 {
		t.Error("a folded folder wasn't deleted")
	}
}
