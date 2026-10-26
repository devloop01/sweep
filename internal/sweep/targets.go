//go:build darwin && arm64

package sweep

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// builtin is the default target list; see targets.conf for the format.
//
//go:embed targets.conf
var builtin string

// DefaultTargets returns the built-in targets.
func DefaultTargets() Targets {
	return NewTargets(defaultEntries())
}

func defaultEntries() []string {
	e, err := parseList(builtin, nil)
	if err != nil {
		panic("built-in targets.conf: " + err.Error()) // caught by TestDefaultTargets
	}
	return e
}

// LoadTargets returns the built-in targets changed by the user's file at
// path. A missing file is not an error; used reports whether it was read.
func LoadTargets(path string) (t Targets, used bool, err error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultTargets(), false, nil
	}
	if err != nil {
		return Targets{}, false, err
	}
	e, err := parseList(string(src), defaultEntries())
	if err != nil {
		return Targets{}, false, fmt.Errorf("%s %w", path, err)
	}
	return NewTargets(e), true, nil
}

// parseList reads src, a .gitignore-style list, and applies it to base:
// each line adds a name, and "!name" removes one that's in the list.
func parseList(src string, base []string) ([]string, error) {
	entries := slices.Clone(base)
	n := 0
	for line := range strings.Lines(src) {
		n++
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, remove := strings.CutPrefix(line, "!")
		name = strings.TrimSuffix(name, "/") // "node_modules/" means the same, as in .gitignore
		if err := checkEntry(name); err != nil {
			return nil, fmt.Errorf("line %d: %q %w", n, line, err)
		}
		i := slices.Index(entries, name)
		switch {
		case remove && i < 0:
			// An error, not a no-op: a typo here would keep deleting a
			// folder the user meant to protect.
			return nil, fmt.Errorf("line %d: can't remove %q: sweep doesn't delete it anyway", n, name)
		case remove:
			entries = slices.Delete(entries, i, i+1)
		case i < 0:
			entries = append(entries, name)
		}
	}
	return entries, nil
}

// checkEntry accepts only a plain folder name or parent/child, so a config
// line can never mean a path, a pattern, or a folder sweep must never enter.
func checkEntry(e string) error {
	if strings.HasPrefix(e, "/") {
		return errors.New("doesn't need a leading /: names match at any depth")
	}
	parts := strings.Split(e, "/")
	if len(parts) > 2 {
		return errors.New("can be a folder name or parent/child, not a longer path")
	}
	for _, p := range parts {
		switch {
		case p == "" || p == "." || p == "..":
			return errors.New("isn't a folder name")
		case p == ".git" || isApp(p):
			return errors.New("is never swept: sweep doesn't enter .git or .app folders")
		case strings.ContainsAny(p, "*?[]{}\\"):
			return errors.New("can't contain wildcards; list each folder name exactly")
		case strings.ContainsFunc(p, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }):
			return errors.New("can't contain spaces or control characters")
		}
	}
	return nil
}

// Targets is the set of folders one run deletes.
type Targets struct {
	names  map[string]bool
	nested []string // "parent/child": only child goes
}

// NewTargets builds Targets from list entries: folder names and
// parent/child pairs.
func NewTargets(entries []string) Targets {
	t := Targets{names: map[string]bool{}}
	for _, e := range entries {
		if !strings.Contains(e, "/") {
			t.names[e] = true
		}
	}
	// A nested target whose parent goes whole is redundant (.vercel/output
	// when .vercel is listed): drop it so nothing is listed twice.
	for _, e := range entries {
		if parent, _, ok := strings.Cut(e, "/"); ok && !t.names[parent] {
			t.nested = append(t.nested, e)
		}
	}
	return t
}

// Match reports whether path names a target folder.
func (t Targets) Match(path string) bool {
	if t.names[filepath.Base(path)] {
		return true
	}
	for _, n := range t.nested {
		if strings.HasSuffix(path, string(filepath.Separator)+n) {
			return true
		}
	}
	return false
}
