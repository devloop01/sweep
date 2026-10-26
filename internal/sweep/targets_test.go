//go:build darwin && arm64

package sweep

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The built-in file parses to exactly these lists. Changing the defaults
// means changing this test too, on purpose.
func TestDefaultLists(t *testing.T) {
	want := Lists{
		Default: []string{"node_modules", ".svelte-kit", ".turbo", ".next", ".nuxt", ".output", ".astro", ".vite",
			".parcel-cache", ".angular", ".docusaurus", ".nx", "storybook-static"},
		Nested: []string{".vercel/output"},
		Build:  []string{"dist", "build", "out"},
		Deploy: []string{".vercel", ".netlify", ".wrangler"},
	}
	if got := DefaultLists(); !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultLists() = %+v\nwant %+v", got, want)
	}
}

func TestUserSectionsReplaceDefaults(t *testing.T) {
	got, err := parseLists(`
# my overrides
[default]
  node_modules
.cache

[nested]
`, DefaultLists())
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultLists()
	if !reflect.DeepEqual(got.Default, []string{"node_modules", ".cache"}) {
		t.Errorf("Default = %q", got.Default)
	}
	if len(got.Nested) != 0 { // an empty section means none
		t.Errorf("Nested = %q, want none", got.Nested)
	}
	if !reflect.DeepEqual(got.Build, def.Build) || !reflect.DeepEqual(got.Deploy, def.Deploy) {
		t.Errorf("sections not in the file changed: %+v", got)
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
		"[build]\ndist\n[build]\nout":    "line 3: section [build] appears twice",
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
	if err != nil || !used || !reflect.DeepEqual(l.Build, []string{"bin"}) {
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
