//go:build darwin && arm64

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
			if code != 0 || !strings.Contains(out, "Delete all of these? [y/N] ") || !strings.Contains(out, "Cancelled.") {
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
		if code != 0 || !strings.Contains(out, "Deleted 3 folders, freed about ") {
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

func TestDryRun(t *testing.T) {
	root := fixture(t)
	code, out, _ := sweepIn(t, "y\n", true, root, "-n") // flag after DIR
	if code != 0 || targetsLeft(root) != 3 {
		t.Fatalf("code %d, left %d", code, targetsLeft(root))
	}
	// Sizes depend on the filesystem's block size, so match on shape: paths
	// left, sizes right-aligned in one column, the total under them.
	want := regexp.MustCompile(`^Scanning ` + regexp.QuoteMeta(root) + ` \.\.\.
a/node_modules +\d+\.\d KB
c/\.svelte-kit +\d+\.\d KB
e/weird\?name/node_modules +\d+\.\d KB
-{37}
3 folders +\d+\.\d KB
Dry run: nothing deleted\.
$`)
	if !want.MatchString(out) {
		t.Errorf("out:\n%s", out)
	}
	// The longest path is 25 runes; every row is path column + 2 + 10.
	for _, l := range strings.Split(out, "\n")[1:6] {
		if utf8.RuneCountInString(l) != 37 {
			t.Errorf("row not 37 wide: %q", l)
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

func TestDeleteFailure(t *testing.T) {
	root := fixture(t)
	locked := filepath.Join(root, "a/node_modules/pkg")
	os.Chmod(locked, 0o500)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	code, out, errOut := sweepIn(t, "", false, "-y", root)
	if code != 1 || !strings.Contains(out, "Deleted 2 of 3 folders, freed about ") || !strings.Contains(errOut, "permission denied") {
		t.Errorf("code %d, out:\n%s\nstderr:\n%s", code, out, errOut)
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
		0: "0.0 KB", 12 * 1024: "12.0 KB", 340*1024*1024 + 512*1024: "340.5 MB",
		1288490189: "1.2 GB",
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
	if code != 0 || !strings.Contains(o.String(), "Using targets from "+cfg) || !strings.Contains(o.String(), "Deleted 3 folders, ") {
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
	if code != 0 || !strings.Contains(out, "Skipped 1 folder that may hold real files") || strings.Contains(out, "p/dist") || !strings.Contains(out, "Deleted 3 folders") {
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
