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

// The built-in file parses to exactly these lists. Changing the defaults
// means changing this test too, on purpose.
func TestDefaultLists(t *testing.T) {
	want := Lists{
		Default: []string{"node_modules", ".svelte-kit", ".turbo", ".next", ".nuxt", ".output", ".astro", ".vite",
			".parcel-cache", ".angular", ".docusaurus", ".nx", ".vinxi", "storybook-static"},
		Nested: []string{".vercel/output"},
		Build:  []string{"dist", "build", "out"},
		Deploy: []string{".vercel", ".netlify", ".wrangler"},
	}
	if got := DefaultLists(); !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultLists() = %+v\nwant %+v", got, want)
	}
}

func TestUserConfigExtendsDefaults(t *testing.T) {
	got, err := parseLists(`
# my additions
[default]
  .cache
node_modules
!.output

[nested]
!.vercel/output

[build]
coverage

[default]
.expo
`, DefaultLists())
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultLists()
	wantDefault := slices.DeleteFunc(slices.Clone(def.Default), func(s string) bool { return s == ".output" })
	wantDefault = append(wantDefault, ".cache", ".expo") // node_modules was already there: not doubled
	if !reflect.DeepEqual(got.Default, wantDefault) {
		t.Errorf("Default = %q\nwant %q", got.Default, wantDefault)
	}
	if len(got.Nested) != 0 {
		t.Errorf("Nested = %q, want .vercel/output removed", got.Nested)
	}
	if !reflect.DeepEqual(got.Build, append(slices.Clone(def.Build), "coverage")) {
		t.Errorf("Build = %q", got.Build)
	}
	if !reflect.DeepEqual(got.Deploy, def.Deploy) {
		t.Errorf("Deploy changed: %q", got.Deploy)
	}
}

// A removal that matches nothing is an error, so a typo can't silently keep
// a folder deletable.
func TestRemovingUnknownNameFails(t *testing.T) {
	for src, want := range map[string]string{
		"[default]\n!.outptu":           `line 2: can't remove ".outptu": it isn't in the [default] list`,
		"[build]\n!node_modules":        `isn't in the [build] list`,
		"[default]\n!":                  "isn't a folder name",
		"[default]\n!src/lib":           "must be a folder name",
		"[default]\n!.output\n!.output": `line 3: can't remove ".output"`,
	} {
		if _, err := parseLists(src, DefaultLists()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseLists(%q) error %v, want it to mention %q", src, err, want)
		}
	}
}

func TestBadConfig(t *testing.T) {
	for src, want := range map[string]string{
		"[default]\nsrc/lib":             "line 2: \"src/lib\" must be a folder name",
		"[nested]\n.vercel":              "must be parent/child",
		"[nested]\na/b/c":                "must be parent/child",
		"[nested]\n../output":            "isn't a folder name",
		"[default]\n..":                  "isn't a folder name",
		"[default]\n.":                   "isn't a folder name",
		"[default]\n/node_modules":       "must be a folder name",
		"[build]\n.git":                  "never swept",
		"[build]\nFoo.APP":               "never swept",
		"[default]\n*.cache":             "wildcards",
		"[default]\nnode_modules # deps": "spaces",
		"[default]\nbad\u202ename":       "control characters",
		"node_modules":                   "line 1: \"node_modules\" is outside a section",
		"[defaults]\nx":                  "unknown section [defaults]",
	} {
		if _, err := parseLists(src, DefaultLists()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseLists(%q) error %v, want it to mention %q", src, err, want)
		}
	}
}

func TestLoadLists(t *testing.T) {
	dir := t.TempDir()
	l, used, err := LoadLists(filepath.Join(dir, "missing.conf"))
	if err != nil || used || !reflect.DeepEqual(l, DefaultLists()) {
		t.Errorf("missing file: %+v %v %v, want defaults", l, used, err)
	}

	p := filepath.Join(dir, "targets.conf")
	must(t, os.WriteFile(p, []byte("[build]\nbin\n"), 0o644))
	l, used, err = LoadLists(p)
	if err != nil || !used || !reflect.DeepEqual(l.Build, []string{"dist", "build", "out", "bin"}) {
		t.Errorf("user file: %+v %v %v", l, used, err)
	}

	must(t, os.WriteFile(p, []byte("[build]\nsrc/x\n"), 0o644))
	if _, _, err := LoadLists(p); err == nil || !strings.Contains(err.Error(), p+" line 2") {
		t.Errorf("bad file: error %v, want path and line", err)
	}
	if _, _, err := LoadLists(dir); err == nil { // unreadable: a folder
		t.Error("reading a folder as config succeeded")
	}
}

// A nested target is dropped only when its own parent goes whole.
func TestNestedDroppedUnderWholeParent(t *testing.T) {
	l := Lists{Nested: []string{".vercel/output", ".cache/tmp"}, Deploy: []string{".vercel"}}
	tg := l.Targets(false, true)
	if !reflect.DeepEqual(tg.nested, []string{".cache/tmp"}) {
		t.Errorf("nested = %q, want only .cache/tmp", tg.nested)
	}
	if tg = l.Targets(false, false); len(tg.nested) != 2 {
		t.Errorf("nested = %q, want both without --deploy", tg.nested)
	}
}
