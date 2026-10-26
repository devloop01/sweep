//go:build darwin && arm64

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/devloop01/sweep/internal/sweep"
)

func TestKeys(t *testing.T) {
	for in, want := range map[string][]string{
		"\x1b[A\x1b[B":     {"up", "down"},
		"\x1bOA\x1bOB":     {"up", "down"},
		"jk gG":            {"down", "up", "space", "home", "end"},
		"\x1b[5~\x1b[6~":   {"pgup", "pgdn"},
		"\x1b[H\x1b[F":     {"home", "end"},
		"q":                {"quit"},
		"\x03":             {"quit"},           // Ctrl-C
		"\x04":             {"quit"},           // Ctrl-D
		"\x1b":             {"quit"},           // Esc on its own
		"\x1b[C\x1b[1;5Dj": {"down"},           // unused sequences are skipped whole
		"x\r\n":            {"enter", "enter"}, // Return is enter; other letters do nothing
		"a":                {"all"},
		" \x1b[B \x1b[B":   {"space", "down", "space", "down"},
	} {
		if got := keys([]byte(in)); !slices.Equal(got, want) {
			t.Errorf("keys(%q) = %q, want %q", in, got, want)
		}
	}
}

func newScreen(n int) *screen {
	s := &screen{}
	for i := range n {
		path := fmt.Sprintf("p%02d/node_modules", i)
		s.items = append(s.items, item{cand: sweep.Candidate{Path: "/r/" + path, Size: int64(n-i) << 20}, path: path})
	}
	return s
}

func TestScreenKeys(t *testing.T) {
	s := newScreen(5)
	for _, k := range []string{"up", "down", "down", "end", "down", "home", "pgdn"} {
		s.key(k)
	}
	if s.cursor != 1 { // with no view drawn yet, a page is one row
		t.Errorf("cursor %d, want 1", s.cursor)
	}
	s.key("home")
	// With nothing selected, enter deletes the highlighted folder.
	del := s.key("enter")
	if len(del) != 1 || del[0].Path != s.items[0].cand.Path || s.items[0].state != deleting {
		t.Fatalf("enter returned %v, state %v", del, s.items[0].state)
	}
	// The cursor stays put, so a held-down enter deletes only one folder.
	if again := s.key("enter"); again != nil || s.cursor != 0 {
		t.Errorf("second enter returned %v, cursor %d", again, s.cursor)
	}
	s.done(del[0].Path, nil)
	s.key("space") // a deleted folder can't be selected
	if s.items[0].state != deleted || s.freed != 5<<20 || s.key("enter") != nil || s.items[0].selected {
		t.Errorf("after delete: %+v freed %d", s.items[0], s.freed)
	}

	// space selects (and unselects); enter deletes only the selection.
	s.key("down")
	s.key("space") // 1
	s.key("down")
	s.key("space") // 2
	s.key("space") // 2 again: unselected
	s.key("down")
	s.key("space") // 3
	del = s.key("enter")
	if len(del) != 2 || del[0].Path != s.items[1].cand.Path || del[1].Path != s.items[3].cand.Path {
		t.Fatalf("enter deleted %v, want items 1 and 3", del)
	}
	if s.items[1].selected || s.items[2].state != ready {
		t.Error("selection not cleared, or an unselected folder touched")
	}
	s.done(del[0].Path, fmt.Errorf("permission denied"))
	s.done(del[1].Path, nil)
	if s.items[1].state != failed || s.message != "permission denied" || s.items[3].state != deleted {
		t.Errorf("after enter: %+v %+v %q", s.items[1], s.items[3], s.message)
	}

	// a selects every folder still there, then unselects them all.
	s.key("all")
	if !s.items[2].selected || !s.items[4].selected || s.items[0].selected || s.items[1].selected {
		t.Errorf("all selected the wrong folders: %+v", s.items)
	}
	s.key("all")
	if slices.ContainsFunc(s.items, func(it item) bool { return it.selected }) {
		t.Error("second a didn't unselect all")
	}

	s.key("up") // folder 2, still there
	s.key("space")
	del = s.key("enter")
	s.key("quit")
	if s.finished() {
		t.Error("finished while a deletion is still running")
	}
	if s.key("space"); s.key("enter") != nil {
		t.Error("deleted after quit")
	}
	s.done(del[0].Path, nil)
	if !s.finished() {
		t.Error("not finished after quit and the last deletion")
	}
	if (&screen{}).key("enter") != nil || (&screen{}).key("end") != nil {
		t.Error("keys on an empty list")
	}
}

// While the scan adds folders (re-sorting the list), the cursor stays on the
// same folder, so space deletes the folder that was highlighted, not
// whatever slid into its row. Finished deletions are matched by path.
func TestScreenCursorFollowsFolder(t *testing.T) {
	s := &screen{scanning: true}
	add := func(name string, size int64) { s.add(sweep.Candidate{Path: "/r/" + name, Size: size}, name) }
	add("a", 3)
	add("b", 1)
	s.key("down") // on b
	add("c", 5)   // sorts to the top: c, a, b
	add("d", 2)   // c, a, d, b
	if s.items[s.cursor].path != "b" {
		t.Fatalf("cursor on %q, want b", s.items[s.cursor].path)
	}
	s.key("space") // select b
	add("f", 6)    // everything moves down a row
	c := s.key("enter")
	if len(c) != 1 || c[0].Path != "/r/b" {
		t.Fatalf("enter deleted %v, want b", c)
	}
	add("e", 4) // b's row moves again before its deletion finishes
	s.done("/r/b", nil)
	if s.items[s.find("/r/b")].state != deleted || s.items[s.find("/r/e")].state != ready {
		t.Error("deletion recorded on the wrong row")
	}
	// The scan's final sizes re-sort too; the cursor still follows b, and a
	// deleted folder keeps the size it was deleted with.
	s.finish([]sweep.Candidate{{Path: "/r/a", Size: 9}, {Path: "/r/b", Size: 8}, {Path: "/r/c", Size: 5}, {Path: "/r/d", Size: 2}, {Path: "/r/e", Size: 4}, {Path: "/r/f", Size: 6}}, 2)
	if s.items[s.cursor].path != "b" || s.items[0].path != "a" || s.scanning || s.skipped != 2 {
		t.Errorf("after finish: cursor on %q, first %q, scanning %v", s.items[s.cursor].path, s.items[0].path, s.scanning)
	}
	if s.items[s.find("/r/b")].cand.Size != 1 || s.total != 9+1+5+2+4+6 {
		t.Errorf("sizes after finish: b %d, total %d", s.items[s.find("/r/b")].cand.Size, s.total)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestScreenView(t *testing.T) {
	s := newScreen(40)
	s.items[3].path = strings.Repeat("very-long-folder/", 10) + "node_modules"
	for _, size := range [][2]int{{80, 24}, {40, 10}, {20, 6}} {
		w, h := size[0], size[1]
		s.cursor = 30
		lines := s.view(env{color: true}, w, h)
		if len(lines) != h {
			t.Errorf("%dx%d: %d lines", w, h, len(lines))
		}
		for _, l := range lines {
			if n := utf8.RuneCountInString(ansi.ReplaceAllString(l, "")); n > w {
				t.Errorf("%dx%d: line %d wide: %q", w, h, n, l)
			}
		}
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "> ") }) {
			t.Errorf("%dx%d: cursor row scrolled out of view", w, h)
		}
	}
	s.cursor = 3
	lines := s.view(env{}, 60, 24)
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "…") && strings.Contains(l, "/node_modules") }) {
		t.Error("long path not cut from the left")
	}
}

// keysThen runs the interactive screen on the fixture and types input once
// the scan has finished.
func keysThen(t *testing.T, input string) (root string, code int, out string) {
	t.Helper()
	root = fixture(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var o, e bytes.Buffer
	code = run([]string{root}, env{
		isTTY: true, cols: 80, size: func() (int, int) { return 80, 24 },
		afterScan: func() {
			go func() {
				pw.Write([]byte(input))
				pw.Close()
			}()
		},
		homes: []string{t.TempDir()}, stdin: pr, stdout: &o, stderr: &e, workers: 4,
	})
	return root, code, o.String()
}

// With nothing to clean the screen closes by itself once the scan ends.
func TestInteractiveNothingToClean(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var o, e bytes.Buffer
	code := run([]string{root}, env{
		isTTY: true, cols: 80, size: func() (int, int) { return 80, 24 },
		homes: []string{t.TempDir()}, stdin: pr, stdout: &o, stderr: &e, workers: 4,
	})
	if code != 0 || !strings.HasSuffix(o.String(), "Nothing to clean.\n") {
		t.Errorf("code %d, out:\n%q", code, o.String())
	}
}

// The list is a/node_modules, c/.svelte-kit, e/weird?name/node_modules
// (biggest first, then by path). g goes to the top: the cursor otherwise
// stays on whichever folder the scan found first.
func TestInteractiveDeletesHighlighted(t *testing.T) {
	root, code, out := keysThen(t, "gj\rq")
	if code != 0 || !strings.Contains(out, "Deleted 1 folder, freed about") {
		t.Errorf("code %d, out:\n%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "c/.svelte-kit")); err == nil {
		t.Error("highlighted folder not deleted")
	}
	if targetsLeft(root) != 2 {
		t.Errorf("%d targets left, want 2", targetsLeft(root))
	}
	if !strings.Contains(out, "\x1b[?1049h") || !strings.HasSuffix(strings.Split(out, "Deleted")[0], "\x1b[?25h\x1b[?1049l") {
		t.Error("alternate screen not entered and left around the summary")
	}
}

func TestInteractiveHeldEnterDeletesOne(t *testing.T) {
	root, _, out := keysThen(t, "g\r\r\r\r\rq")
	if targetsLeft(root) != 2 || !strings.Contains(out, "Deleted 1 folder,") {
		t.Errorf("left %d, out:\n%q", targetsLeft(root), out)
	}
}

func TestInteractiveDeletesSelection(t *testing.T) {
	root, _, out := keysThen(t, "g jj \rq") // select a and e/…, delete both
	if _, err := os.Stat(filepath.Join(root, "c/.svelte-kit")); err != nil || targetsLeft(root) != 1 || !strings.Contains(out, "Deleted 2 folders,") {
		t.Errorf("left %d, out:\n%q", targetsLeft(root), out)
	}
	root, _, out = keysThen(t, "a\rq") // everything
	if targetsLeft(root) != 0 || !strings.Contains(out, "Deleted 3 folders,") {
		t.Errorf("select all: left %d, out:\n%q", targetsLeft(root), out)
	}
}

func TestInteractiveQuitDeletesNothing(t *testing.T) {
	for _, input := range []string{"q", "", "\x03", "jjk\x1b", "g  a a q"} { // selecting alone deletes nothing
		root, code, out := keysThen(t, input)
		if code != 0 || targetsLeft(root) != 3 || !strings.Contains(out, "Nothing deleted.") {
			t.Errorf("%q: code %d, left %d, out:\n%q", input, code, targetsLeft(root), out)
		}
	}
}
