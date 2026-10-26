//go:build darwin && arm64

package sweep

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The built-in file parses to exactly this list. Changing the defaults
// means changing this test too, on purpose.
func TestDefaultTargets(t *testing.T) {
	want := []string{"node_modules", ".svelte-kit", ".turbo", ".next", ".nuxt", ".output", ".astro", ".vite",
		".parcel-cache", ".angular", ".docusaurus", ".nx", ".vinxi", "storybook-static", "dist", "build", "out", ".vercel/output"}
	if got := defaultEntries(); !slices.Equal(got, want) {
		t.Errorf("defaultEntries() = %q\nwant %q", got, want)
	}
}

func TestUserListAddsAndRemoves(t *testing.T) {
	got, err := parseList(`
# my additions
  .cache
coverage/
node_modules

# never these
!.output
!.vercel/output
`, defaultEntries())
	if err != nil {
		t.Fatal(err)
	}
	want := slices.DeleteFunc(defaultEntries(), func(s string) bool { return s == ".output" || s == ".vercel/output" })
	want = append(want, ".cache", "coverage") // node_modules was already there: not doubled
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestBadUserList(t *testing.T) {
	for src, want := range map[string]string{
		"src/lib/x":          `line 1: "src/lib/x" can be a folder name or parent/child`,
		"/node_modules":      "doesn't need a leading /",
		"..":                 "isn't a folder name",
		".":                  "isn't a folder name",
		"!":                  "isn't a folder name",
		"a//b":               "not a longer path",
		".git":               "never swept",
		"Foo.APP":            "never swept",
		"x/.git":             "never swept",
		"*.cache":            "wildcards",
		"**/dist":            "wildcards",
		"[default]":          "wildcards",
		"node_modules # dep": "spaces",
		"bad\u202ename":      "control characters",
		"ok\n!.outptu":       `line 2: can't remove ".outptu": sweep doesn't delete it anyway`,
		"!.wrangler":         `can't remove ".wrangler"`,
		"!.output\n!.output": `line 2: can't remove ".output"`,
	} {
		if _, err := parseList(src, defaultEntries()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseList(%q) error %v, want it to mention %q", src, err, want)
		}
	}
}

func TestLoadTargets(t *testing.T) {
	dir := t.TempDir()
	tg, used, err := LoadTargets(filepath.Join(dir, "missing.conf"))
	if err != nil || used || !reflect.DeepEqual(tg, DefaultTargets()) {
		t.Errorf("missing file: %+v %v %v, want defaults", tg, used, err)
	}

	p := filepath.Join(dir, "targets.conf")
	must(t, os.WriteFile(p, []byte("dist\n!.output\n"), 0o644))
	tg, used, err = LoadTargets(p)
	if err != nil || !used || !tg.Match("/x/dist") || tg.Match("/x/.output") || !tg.Match("/x/node_modules") {
		t.Errorf("user file: %+v %v %v", tg, used, err)
	}

	must(t, os.WriteFile(p, []byte("dist\nsrc/a/b\n"), 0o644))
	if _, _, err := LoadTargets(p); err == nil || !strings.Contains(err.Error(), p+" line 2") {
		t.Errorf("bad file: error %v, want path and line", err)
	}
	if _, _, err := LoadTargets(dir); err == nil { // unreadable: a folder
		t.Error("reading a folder as config succeeded")
	}
}

// A nested target is dropped only when its own parent goes whole.
func TestNestedDroppedUnderWholeParent(t *testing.T) {
	tg := NewTargets([]string{".vercel/output", ".cache/tmp", ".vercel"})
	if !slices.Equal(tg.nested, []string{".cache/tmp"}) {
		t.Errorf("nested = %q, want only .cache/tmp", tg.nested)
	}
	if tg = NewTargets([]string{".vercel/output", ".cache/tmp"}); len(tg.nested) != 2 {
		t.Errorf("nested = %q, want both", tg.nested)
	}
}
