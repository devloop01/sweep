//go:build darwin && arm64

package main

import (
	"bytes"
	"fmt"
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
		"\x03":             {"quit"}, // Ctrl-C
		"\x04":             {"quit"}, // Ctrl-D
		"\x1b":             {"quit"}, // Esc on its own
		"\x1b[C\x1b[1;5Dj": {"down"}, // unused sequences are skipped whole
		"x\r\n":            nil,
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
		s.items = append(s.items, item{cand: sweep.Candidate{Size: int64(n-i) << 20}, path: fmt.Sprintf("p%02d/node_modules", i)})
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
	if i := s.key("space"); i != 0 || s.items[0].state != deleting {
		t.Fatalf("space returned %d, state %v", i, s.items[0].state)
	}
	// The cursor stays put, so a held-down space deletes only one folder.
	if i := s.key("space"); i != -1 || s.cursor != 0 {
		t.Errorf("second space returned %d, cursor %d", i, s.cursor)
	}
	s.done(0, nil)
	if s.items[0].state != deleted || s.freed != 5<<20 || s.key("space") != -1 {
		t.Errorf("after delete: %+v freed %d", s.items[0], s.freed)
	}
	s.key("down")
	s.key("space")
	s.done(1, fmt.Errorf("permission denied"))
	if s.items[1].state != failed || s.message != "permission denied" || s.freed != 5<<20 {
		t.Errorf("after failure: %+v %q", s.items[1], s.message)
	}
	s.key("down")
	i := s.key("space")
	s.key("quit")
	if s.finished() {
		t.Error("finished while a deletion is still running")
	}
	if s.key("down"); s.key("space") != -1 {
		t.Error("deleted after quit")
	}
	s.done(i, nil)
	if !s.finished() {
		t.Error("not finished after quit and the last deletion")
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

// keysThen runs the interactive screen on the fixture with the given input.
func keysThen(t *testing.T, input string) (root string, code int, out string) {
	t.Helper()
	root = fixture(t)
	var o, e bytes.Buffer
	code = run([]string{root}, env{
		isTTY: true, cols: 80, size: func() (int, int) { return 80, 24 },
		homes: []string{t.TempDir()}, stdin: strings.NewReader(input), stdout: &o, stderr: &e, workers: 4,
	})
	return root, code, o.String()
}

// The list is a/node_modules, c/.svelte-kit, e/weird?name/node_modules
// (biggest first, then by path).
func TestInteractiveDeletesHighlighted(t *testing.T) {
	root, code, out := keysThen(t, "j q")
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

func TestInteractiveHeldSpaceDeletesOne(t *testing.T) {
	root, _, out := keysThen(t, "     q")
	if targetsLeft(root) != 2 || !strings.Contains(out, "Deleted 1 folder,") {
		t.Errorf("left %d, out:\n%q", targetsLeft(root), out)
	}
}

func TestInteractiveQuitDeletesNothing(t *testing.T) {
	for _, input := range []string{"q", "", "\x03", "jjk\x1b"} {
		root, code, out := keysThen(t, input)
		if code != 0 || targetsLeft(root) != 3 || !strings.Contains(out, "Nothing deleted.") {
			t.Errorf("%q: code %d, left %d, out:\n%q", input, code, targetsLeft(root), out)
		}
	}
}
