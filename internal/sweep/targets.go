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

// builtin holds the default target lists; see targets.conf for the format.
//
//go:embed targets.conf
var builtin string

// Lists are the folder names sweep targets, by section of targets.conf.
type Lists struct {
	Default []string // always
	Nested  []string // always; "parent/child", only child goes
	Build   []string // with --build
	Deploy  []string // with --deploy
}

// DefaultLists returns the built-in lists.
func DefaultLists() Lists {
	l, err := parseLists(builtin, Lists{})
	if err != nil {
		panic("built-in targets.conf: " + err.Error()) // caught by TestDefaultLists
	}
	return l
}

// LoadLists returns the built-in lists extended by the user's file at path.
// A missing file is not an error; used reports whether it was read.
func LoadLists(path string) (l Lists, used bool, err error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultLists(), false, nil
	}
	if err != nil {
		return Lists{}, false, err
	}
	l, err = parseLists(string(src), DefaultLists())
	if err != nil {
		return Lists{}, false, fmt.Errorf("%s %w", path, err)
	}
	return l, true, nil
}

// parseLists reads src in the targets.conf format and applies it to base:
// each name is added to its section, and "!name" removes a name already in it.
func parseLists(src string, base Lists) (Lists, error) {
	sections := map[string]*[]string{
		"default": &base.Default, "nested": &base.Nested, "build": &base.Build, "deploy": &base.Deploy,
	}
	var cur *[]string
	var curName string
	n := 0
	for line := range strings.Lines(src) {
		n++
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			curName = line[1 : len(line)-1]
			p, ok := sections[curName]
			if !ok {
				return Lists{}, fmt.Errorf("line %d: unknown section [%s]; use [default], [nested], [build] or [deploy]", n, curName)
			}
			cur = p
		case cur == nil:
			return Lists{}, fmt.Errorf("line %d: %q is outside a section; start with a [section] line", n, line)
		default:
			name, remove := strings.CutPrefix(line, "!")
			if err := checkEntry(name, curName == "nested"); err != nil {
				return Lists{}, fmt.Errorf("line %d: %q %w", n, line, err)
			}
			i := slices.Index(*cur, name)
			switch {
			case remove && i < 0:
				// An error, not a no-op: a typo here would keep deleting a
				// folder the user meant to protect.
				return Lists{}, fmt.Errorf("line %d: can't remove %q: it isn't in the [%s] list", n, name, curName)
			case remove:
				*cur = slices.Delete(*cur, i, i+1)
			case i < 0:
				*cur = append(*cur, name)
			}
		}
	}
	return base, nil
}

// checkEntry accepts only plain folder names (parent/child for nested), so
// a config line can never mean a path, a pattern, or a folder sweep must
// never enter.
func checkEntry(e string, nested bool) error {
	parts := strings.Split(e, "/")
	if nested && len(parts) != 2 {
		return errors.New("must be parent/child, like .vercel/output")
	}
	if !nested && len(parts) != 1 {
		return errors.New("must be a folder name, not a path (nested folders go in [nested])")
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
	nested []string
}

// Targets returns what a run with these flags deletes.
func (l Lists) Targets(build, deploy bool) Targets {
	t := Targets{names: map[string]bool{}}
	add := func(names []string) {
		for _, n := range names {
			t.names[n] = true
		}
	}
	add(l.Default)
	if build {
		add(l.Build)
	}
	if deploy {
		add(l.Deploy)
	}
	// A nested target whose parent goes whole is redundant (with --deploy,
	// .vercel/output inside .vercel): drop it so nothing is listed twice.
	for _, n := range l.Nested {
		if parent, _, _ := strings.Cut(n, "/"); !t.names[parent] {
			t.nested = append(t.nested, n)
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
