//go:build darwin && arm64

package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStatusFitsWidth(t *testing.T) {
	long := strings.Repeat("deep/", 40) + "node_modules"
	for _, width := range []int{120, 80, 40, 20, 5, 0} {
		s := status(1234, long, width)
		if n := utf8.RuneCountInString(s); n > max(width-1, 0) {
			t.Errorf("width %d: %d runes: %q", width, n, s)
		}
	}
	if s := status(7, "web/app", 80); s != "Scanning… 7 folders  web/app" {
		t.Errorf("short path: %q", s)
	}
	if s := status(7, strings.Repeat("x", 100)+"/end", 40); !strings.HasPrefix(s, "Scanning… 7 folders  …") || !strings.HasSuffix(s, "/end") {
		t.Errorf("long path should keep its end: %q", s)
	}
	if s := status(1, "a\nb", 80); strings.Contains(s, "\n") {
		t.Errorf("control characters in status: %q", s)
	}
}

func TestSizeStyle(t *testing.T) {
	for b, want := range map[int64]string{
		2 << 30: red + bold, 1 << 30: red + bold, 300 << 20: yellow, 100 << 20: yellow,
		50 << 20: "", 1 << 20: "", 512 << 10: dim, 0: dim,
	} {
		if got := sizeStyle(b); got != want {
			t.Errorf("sizeStyle(%d) = %q, want %q", b, got, want)
		}
	}
}

func TestColorOK(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, tc := range []struct {
		tty  bool
		vars map[string]string
		want bool
	}{
		{true, nil, true},
		{false, nil, false},
		{true, map[string]string{"NO_COLOR": "1"}, false},
		{true, map[string]string{"TERM": "dumb"}, false},
		{true, map[string]string{"TERM": "xterm-256color"}, true},
	} {
		if got := colorOK(tc.tty, env(tc.vars)); got != tc.want {
			t.Errorf("colorOK(%v, %v) = %v", tc.tty, tc.vars, got)
		}
	}
}

// With color on, sizes and the total are styled; with it off (pipes, tests)
// the output has no escape codes at all.
func TestColoredOutput(t *testing.T) {
	root := fixture(t)
	for _, color := range []bool{true, false} {
		var o, e bytes.Buffer
		run([]string{"-n", root}, env{color: color, homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4})
		hasEsc := strings.Contains(o.String(), "\x1b[")
		if hasEsc != color {
			t.Errorf("color=%v but escapes=%v:\n%q", color, hasEsc, o.String())
		}
		if color && !strings.Contains(o.String(), bold+"3 folders") {
			t.Errorf("total not bold:\n%q", o.String())
		}
	}
}

// The status line goes to stderr only, and is erased when the scan ends.
func TestLiveStatusIsErased(t *testing.T) {
	root := fixture(t)
	var o, e bytes.Buffer
	run([]string{"-n", root}, env{live: true, homes: []string{t.TempDir()}, stdout: &o, stderr: &e, workers: 4})
	if !strings.HasSuffix(e.String(), "\r\x1b[K") {
		t.Errorf("stderr doesn't end by erasing the status line: %q", e.String())
	}
	if strings.Contains(o.String(), "\x1b[") || strings.Contains(o.String(), "Scanning…") {
		t.Errorf("status leaked into stdout:\n%q", o.String())
	}
}
