//go:build darwin && arm64

package sweep

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Fixtures shared by scan_test and remove_test. All paths are relative to
// a temp dir; the swept DIR is always "root".
var cases = []struct {
	name          string
	build, deploy bool
	tree          []string // "x/" is a folder, "x -> y" a symlink to y, anything else a file
	listed        []string // what Scan finds; Remove must delete exactly these
	kept          []string // must still exist after Remove
}{
	{
		name:   "basic",
		tree:   []string{"root/a/node_modules/pkg/f", "root/c/.svelte-kit/"},
		listed: []string{"root/a/node_modules", "root/c/.svelte-kit"},
		kept:   []string{"root/a", "root/c"},
	},
	{
		name:   "symlink inside a target is removed as a link",
		tree:   []string{"outside/precious/file", "root/a/node_modules/linked -> outside/precious"},
		listed: []string{"root/a/node_modules"},
		kept:   []string{"outside/precious/file"},
	},
	{
		name: "symlink named like a target",
		tree: []string{"outside/precious/file", "root/d/node_modules -> outside/precious"},
		kept: []string{"root/d/node_modules", "outside/precious/file"},
	},
	{
		name: "file named like a target",
		tree: []string{"root/b/node_modules"},
		kept: []string{"root/b/node_modules"},
	},
	{
		name: ".git is never entered",
		tree: []string{"root/.git/node_modules/f"},
		kept: []string{"root/.git/node_modules/f"},
	},
	{
		name: "app bundles are never entered",
		tree: []string{"root/My.app/Contents/node_modules/f", "root/Other.APP/node_modules/f"},
		kept: []string{"root/My.app/Contents/node_modules/f", "root/Other.APP/node_modules/f"},
	},
	{
		name: "deploy folders kept by default",
		tree: []string{"root/a/.wrangler/state/db", "root/a/.vercel/project.json", "root/a/.netlify/state.json"},
		kept: []string{"root/a/.wrangler/state/db", "root/a/.vercel/project.json", "root/a/.netlify/state.json"},
	},
	{
		name:   "deploy folders deleted with --deploy",
		deploy: true,
		tree:   []string{"root/a/.wrangler/state/db", "root/a/.vercel/project.json", "root/a/.netlify/state.json"},
		listed: []string{"root/a/.wrangler", "root/a/.vercel", "root/a/.netlify"},
		kept:   []string{"root/a"},
	},
	{
		name:   ".vercel/output goes, project.json stays",
		tree:   []string{"root/a/.vercel/output/functions/f", "root/a/.vercel/project.json"},
		listed: []string{"root/a/.vercel/output"},
		kept:   []string{"root/a/.vercel/project.json"},
	},
	{
		name: "output alone is not a target",
		tree: []string{"root/g/output/", "root/h/notvercel/output/"},
		kept: []string{"root/g/output", "root/h/notvercel/output"},
	},
	{
		name:   "nested target inside a match is not listed",
		tree:   []string{"root/a/node_modules/pkg/.vercel/output/f"},
		listed: []string{"root/a/node_modules"},
		kept:   []string{"root/a"},
	},
	{
		name:   "--deploy lists .vercel once, not .vercel/output too",
		deploy: true,
		tree:   []string{"root/a/.vercel/output/f"},
		listed: []string{"root/a/.vercel"},
	},
	{
		name: "build folders kept by default",
		tree: []string{"root/f/build/src/", "root/f/dist/x", "root/f/out/"},
		kept: []string{"root/f/build/src", "root/f/dist/x", "root/f/out"},
	},
	{
		name:   "build folders deleted with --build",
		build:  true,
		tree:   []string{"root/f/build/src/", "root/f/dist/x", "root/f/out/"},
		listed: []string{"root/f/build", "root/f/dist", "root/f/out"},
		kept:   []string{"root/f"},
	},
	{
		name:   "odd names",
		tree:   []string{"root/e/weird\nname/node_modules/", "root/-x y/ünï\u202e/node_modules/f"},
		listed: []string{"root/e/weird\nname/node_modules", "root/-x y/ünï\u202e/node_modules"},
		kept:   []string{"root/e/weird\nname", "root/-x y/ünï\u202e"},
	},
	{
		name:   "nested node_modules: only the outer one",
		tree:   []string{"root/node_modules/x/node_modules/f"},
		listed: []string{"root/node_modules"},
	},
}

// build creates entries under tmp and returns the resolved tmp and root.
func build(t *testing.T, tree ...string) (tmp, root string) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(tmp, "root")
	must(t, os.MkdirAll(root, 0o755))
	for _, e := range tree {
		p, target, isLink := strings.Cut(e, " -> ")
		p = filepath.Join(tmp, p)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		switch {
		case isLink:
			must(t, os.Symlink(filepath.Join(tmp, target), p))
		case strings.HasSuffix(e, "/"):
			must(t, os.MkdirAll(p, 0o755))
		default:
			must(t, os.WriteFile(p, []byte("data"), 0o644))
		}
	}
	return tmp, root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// rels returns the candidates' paths relative to tmp, sorted.
func rels(t *testing.T, tmp string, cands []Candidate) []string {
	var out []string
	for _, c := range cands {
		r, err := filepath.Rel(tmp, c.Path)
		must(t, err)
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

func TestScan(t *testing.T) {
	for _, w := range []int{1, 4} { // 1 exercises the inline walk
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/workers=%d", tc.name, w), func(t *testing.T) {
				tmp, root := build(t, tc.tree...)
				got := rels(t, tmp, Scan(root, DefaultLists().Targets(tc.build, tc.deploy), w))
				if !slices.Equal(got, sorted(tc.listed)) {
					t.Errorf("listed %q, want %q", got, sorted(tc.listed))
				}
				for _, e := range tc.tree { // scanning deletes nothing
					p, _, _ := strings.Cut(e, " -> ")
					if !exists(filepath.Join(tmp, p)) {
						t.Errorf("%q gone after Scan", p)
					}
				}
			})
		}
	}
}

func TestScanOrder(t *testing.T) {
	_, root := build(t, "root/small/node_modules/", "root/big/node_modules/", "root/b/.next/", "root/a/.next/")
	must(t, os.WriteFile(filepath.Join(root, "big/node_modules/f"), make([]byte, 64<<10), 0o644))
	var got []string
	for _, c := range Scan(root, DefaultLists().Targets(false, false), 4) {
		got = append(got, strings.TrimPrefix(c.Path, root+"/"))
	}
	want := []string{"big/node_modules", "a/.next", "b/.next", "small/node_modules"}
	if !slices.Equal(got, want) {
		t.Errorf("order %q, want %q", got, want)
	}
}

// Sizes are allocated blocks like du, not apparent size: a sparse 1 GB file
// takes almost nothing.
func TestSizeIsAllocated(t *testing.T) {
	_, root := build(t, "root/a/node_modules/")
	f, err := os.Create(filepath.Join(root, "a/node_modules/sparse"))
	must(t, err)
	must(t, f.Truncate(1<<30))
	f.Close()
	must(t, os.WriteFile(filepath.Join(root, "a/node_modules/real"), make([]byte, 100_000), 0o644))

	c := Scan(root, DefaultLists().Targets(false, false), 4)
	if len(c) != 1 || c[0].Size < 100_000 || c[0].Size > 1<<20 {
		t.Errorf("got %+v, want one candidate of roughly 100 KB", c)
	}
}

func TestHardLinksCountedOnce(t *testing.T) {
	_, root := build(t, "root/a/node_modules/", "root/b/node_modules/")
	a, b := filepath.Join(root, "a/node_modules"), filepath.Join(root, "b/node_modules")
	must(t, os.WriteFile(filepath.Join(a, "f"), make([]byte, 1<<20), 0o644))
	must(t, os.Link(filepath.Join(a, "f"), filepath.Join(b, "f")))

	blocks := func(p string) int64 { return measure(p).own } // dir blocks only: f has 2 links
	file := measure(a).shared
	if len(file) != 1 {
		t.Fatalf("want 1 shared inode, got %v", file)
	}
	var fileSize int64
	for _, s := range file {
		fileSize = s
	}
	want := blocks(a) + blocks(b) + fileSize

	first := Scan(root, DefaultLists().Targets(false, false), 4)
	var total int64
	for _, c := range first {
		total += c.Size
	}
	if total != want {
		t.Errorf("total %d, want %d (file counted once)", total, want)
	}
	if first[0].Path != a { // the shared file is charged to the first path
		t.Errorf("biggest is %s, want %s", first[0].Path, a)
	}
	for range 5 {
		if again := Scan(root, DefaultLists().Targets(false, false), 4); !slices.Equal(again, first) {
			t.Fatalf("sizes not deterministic: %+v vs %+v", again, first)
		}
	}
}

func TestVet(t *testing.T) {
	tmp, root := build(t,
		"root/a/node_modules/", "root/b/node_modules", "outside/node_modules/",
		"root/.git/x/node_modules/", "root/X.app/node_modules/", "root/a/src/",
		"root/l -> outside", "root/..x/node_modules/",
	)
	tg := DefaultLists().Targets(false, false)
	ok := map[string]bool{
		"root/a/node_modules":          true,
		"root":                         false, // DIR itself
		"outside/node_modules":         false, // outside DIR
		"root/../outside/node_modules": false,
		"root/b/node_modules":          false, // a file
		"root/.git/x/node_modules":     false,
		"root/X.app/node_modules":      false,
		"root/a/src":                   false, // not a target
		"root/l/node_modules":          false, // through a symlinked parent
		"root/a/missing/node_modules":  false,
		"root/..x/node_modules":        false, // ponytail: rel starting with ".." is refused, even as a name
	}
	for rel, want := range ok {
		if got := Vet(root, filepath.Join(tmp, rel), tg); got != want {
			t.Errorf("Vet(%q) = %v, want %v", rel, got, want)
		}
	}
}

// mount attaches a small APFS disk image at dir. It's detached before the
// temp dirs are removed (cleanups run last-in, first-out).
func mount(t *testing.T, dir string) {
	t.Helper()
	img := filepath.Join(t.TempDir(), "vol.dmg")
	if out, err := exec.Command("hdiutil", "create", "-size", "4m", "-fs", "APFS", "-volname", "sweeptest", img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil create: %v\n%s", err, out)
	}
	must(t, os.MkdirAll(dir, 0o755))
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-noautoopen", "-mountpoint", dir, img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil attach: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("hdiutil", "detach", "-force", dir).CombinedOutput(); err != nil {
			t.Errorf("hdiutil detach: %v\n%s", err, out)
		}
	})
}

// Another volume mounted inside DIR (a share, a disk image) is never swept,
// and neither is a target with something mounted inside it.
func TestScanStaysOnVolume(t *testing.T) {
	_, root := build(t, "root/a/node_modules/f", "root/b/node_modules/f")
	share := filepath.Join(root, "share")
	mount(t, share)
	must(t, os.MkdirAll(filepath.Join(share, "proj/node_modules"), 0o755))
	must(t, os.WriteFile(filepath.Join(share, "proj/node_modules/f"), []byte("remote"), 0o644))
	mount(t, filepath.Join(root, "b/node_modules/inner"))

	tg := DefaultLists().Targets(false, false)
	cands := Scan(root, tg, 4)
	if len(cands) != 1 || cands[0].Path != filepath.Join(root, "a/node_modules") {
		t.Fatalf("listed %+v, want only a/node_modules", cands)
	}
	if Vet(root, filepath.Join(share, "proj/node_modules"), tg) {
		t.Error("Vet allowed a folder on another volume")
	}
	r := open(t, root)
	Remove(r, tg, append(cands, Candidate{Path: filepath.Join(share, "proj/node_modules")}), 4)
	for _, p := range []string{"share/proj/node_modules/f", "b/node_modules/f"} {
		if !exists(filepath.Join(root, p)) {
			t.Errorf("%s was deleted", p)
		}
	}
}
