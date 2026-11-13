//go:build darwin && arm64

package sweep

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// newTrash is an empty folder to use as the Trash, on the same volume as
// the trees build makes.
func newTrash(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	return d
}

// inTrash lists the names in trash, sorted.
func inTrash(t *testing.T, trash string) []string {
	t.Helper()
	entries, err := os.ReadDir(trash)
	must(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRemove(t *testing.T) {
	for _, w := range []int{1, 4} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/workers=%d", tc.name, w), func(t *testing.T) {
				trash := newTrash(t)
				tmp, root := build(t, tc.tree...)
				tg := targetsFor(t, tc.config)
				cands := scanned(root, tg, w)
				deleted, errs := Remove(open(t, root), tg, cands, w, trash)
				if len(errs) > 0 {
					t.Fatal(errs)
				}
				if got := rels(t, tmp, deleted); !slices.Equal(got, sorted(tc.listed)) {
					t.Errorf("deleted %q, want %q", got, sorted(tc.listed))
				}
				for _, p := range tc.listed {
					if exists(filepath.Join(tmp, p)) {
						t.Errorf("%q still exists", p)
					}
				}
				for _, p := range tc.kept {
					if !exists(filepath.Join(tmp, p)) {
						t.Errorf("%q was deleted", p)
					}
				}
				if len(inTrash(t, trash)) != len(tc.listed) {
					t.Errorf("Trash holds %q, want %d folders", inTrash(t, trash), len(tc.listed))
				}
			})
		}
	}
}

// Moved folders keep everything in them, get a name that says where they
// came from, and never replace what's already in the Trash.
func TestRemoveToTrash(t *testing.T) {
	_, root := build(t, "root/web/node_modules/pkg/index.js", "root/web/.next/cache", "root/api/src/node_modules/f", "root/node_modules/f")
	trash := newTrash(t)
	must(t, os.MkdirAll(filepath.Join(trash, "root › web › node_modules/old"), 0o755))
	tg := DefaultTargets()
	deleted, errs := Remove(open(t, root), tg, scanned(root, tg, 4), 4, trash)
	if len(deleted) != 4 || len(errs) != 0 {
		t.Fatalf("moved %v, errs %v", deleted, errs)
	}
	want := []string{"root › api › src › node_modules", "root › node_modules", "root › web › .next", "root › web › node_modules", "root › web › node_modules 2"}
	if got := inTrash(t, trash); !slices.Equal(got, want) {
		t.Errorf("Trash holds %q, want %q", got, want)
	}
	for _, p := range []string{"root › web › node_modules/old", "root › web › node_modules 2/pkg/index.js", "root › web › .next/cache", "root › api › src › node_modules/f"} {
		if !exists(filepath.Join(trash, p)) {
			t.Errorf("%s isn't in the Trash", p)
		}
	}
	if !exists(filepath.Join(root, "web")) || !exists(filepath.Join(root, "api/src")) {
		t.Error("moved more than the targets")
	}
}

// A folder that can't be moved is reported, and nothing in it is lost.
func TestRemoveToTrashReportsFailures(t *testing.T) {
	_, root := build(t, "root/a/node_modules/f", "root/b/node_modules/f")
	a := filepath.Join(root, "a")
	must(t, os.Chmod(a, 0o500)) // a/node_modules can't leave a
	t.Cleanup(func() { os.Chmod(a, 0o755) })

	tg := DefaultTargets()
	deleted, errs := Remove(open(t, root), tg, scanned(root, tg, 4), 4, newTrash(t))
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0].Path, "b/node_modules") {
		t.Errorf("moved %v, want only b/node_modules", deleted)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "move to Trash") {
		t.Errorf("errs %v, want 1 about the Trash", errs)
	}
	if !exists(filepath.Join(a, "node_modules/f")) {
		t.Error("lost a file of a folder that couldn't be moved")
	}
}

func TestTrashName(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		n    int
		want string
	}{
		{"node_modules", 1, "code › node_modules"},
		{"web/node_modules", 1, "code › web › node_modules"},
		{"mono/packages/ui/.turbo", 1, "code › mono › packages › ui › .turbo"},
		{"web/node_modules", 3, "code › web › node_modules 3"},
	} {
		if got := trashName("/Users/me/code", tc.rel, tc.n); got != tc.want {
			t.Errorf("trashName(%q, %d) = %q, want %q", tc.rel, tc.n, got, tc.want)
		}
	}
	// A deep path keeps its end and fits a file name.
	deep := strings.Repeat("ünïcödé/", 40) + "app/node_modules"
	for n, end := range map[int]string{1: " › app › node_modules", 12: " › app › node_modules 12"} {
		got := trashName("/r", deep, n)
		if len(got) > maxName || !utf8.ValidString(got) || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, end) {
			t.Errorf("trashName(deep, %d) = %q (%d bytes)", n, got, len(got))
		}
	}
	long := strings.Repeat("x", maxName)
	if got := trashName("/r", "a/"+long, 2); len(got) > maxName || !strings.HasSuffix(got, "xx 2") {
		t.Errorf("trashName(long, 2) = %q (%d bytes)", got, len(got))
	}
}

// FindTrash picks ~/.Trash for a folder on the home folder's disk, makes
// it if it's missing, and refuses one that isn't a real folder.
func TestFindTrash(t *testing.T) {
	_, root := build(t, "root/a/")
	home := newTrash(t)
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".Trash")
	for range 2 { // missing, then there
		got, err := FindTrash(root)
		if err != nil || got != want {
			t.Fatalf("FindTrash = %q, %v; want %q", got, err, want)
		}
		if fi, err := os.Lstat(want); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Fatalf("~/.Trash is %v, %v; want a 0700 folder", fi, err)
		}
	}

	must(t, os.Remove(want))
	must(t, os.Symlink(root, want))
	if got, err := FindTrash(root); err == nil {
		t.Errorf("FindTrash = %q for a ~/.Trash that's a symlink", got)
	}
	must(t, os.Remove(want))
	must(t, os.WriteFile(want, nil, 0o644))
	if got, err := FindTrash(root); err == nil {
		t.Errorf("FindTrash = %q for a ~/.Trash that's a file", got)
	}
}

// A candidate swapped for a symlink between scan and delete is not deleted.
func TestRemoveRevetsSymlinkedCandidate(t *testing.T) { removeRevetsSymlinkedCandidate(t, newTrash(t)) }

func removeRevetsSymlinkedCandidate(t *testing.T, trash string) {
	tmp, root := build(t, "root/a/node_modules/f", "outside/precious/file")
	tg := DefaultTargets()
	cands := scanned(root, tg, 4)

	nm := filepath.Join(root, "a/node_modules")
	must(t, os.RemoveAll(nm))
	must(t, os.Symlink(filepath.Join(tmp, "outside/precious"), nm))

	deleted, errs := Remove(open(t, root), tg, cands, 4, trash)
	if len(deleted) != 0 || len(errs) != 0 {
		t.Errorf("deleted %v, errs %v", deleted, errs)
	}
	if !exists(nm) || !exists(filepath.Join(tmp, "outside/precious/file")) {
		t.Error("symlink or its target was deleted")
	}
}

// A parent swapped for a symlink must not lead the delete outside DIR.
func TestRemoveRevetsSymlinkedParent(t *testing.T) { removeRevetsSymlinkedParent(t, newTrash(t)) }

func removeRevetsSymlinkedParent(t *testing.T, trash string) {
	tmp, root := build(t, "root/a/node_modules/f", "outside/node_modules/precious")
	tg := DefaultTargets()
	cands := scanned(root, tg, 4)

	a := filepath.Join(root, "a")
	must(t, os.RemoveAll(a))
	must(t, os.Symlink(filepath.Join(tmp, "outside"), a))

	deleted, _ := Remove(open(t, root), tg, cands, 4, trash)
	if len(deleted) != 0 || !exists(filepath.Join(tmp, "outside/node_modules/precious")) {
		t.Errorf("deleted outside DIR: %v", deleted)
	}
}

// Candidates that were never vetted against this root are refused.
func TestRemoveRefusesOutsideRoot(t *testing.T) {
	trash := newTrash(t)
	tmp, root := build(t, "outside/node_modules/precious", "root/x/")
	out := filepath.Join(tmp, "outside/node_modules")
	tg := DefaultTargets()
	for _, p := range []string{out, root} {
		if Vet(root, p, tg) {
			t.Errorf("Vet allowed %s", p)
		}
	}
	deleted, _ := Remove(open(t, root), tg, []Candidate{at(t, out), at(t, root)}, 4, trash)
	if len(deleted) != 0 || !exists(out) || !exists(root) {
		t.Errorf("deleted %v", deleted)
	}
}

// A candidate that didn't come from Scan has no scanned folder to compare
// with, so it's refused even when its path passes Vet.
func TestRemoveRefusesCandidateNotFromScan(t *testing.T) {
	_, root := build(t, "root/a/node_modules/f")
	p := filepath.Join(root, "a/node_modules")
	tg := DefaultTargets()
	if !Vet(root, p, tg) {
		t.Fatal("Vet refused a plain node_modules; the test proves nothing")
	}
	deleted, errs := Remove(open(t, root), tg, []Candidate{{Path: p}}, 4, newTrash(t))
	if len(deleted) != 0 || len(errs) != 0 || !exists(filepath.Join(p, "f")) {
		t.Errorf("deleted %v (errors %v)", deleted, errs)
	}
}

// at returns a Candidate carrying the identity of the folder at path, as if
// Scan had listed it, so Remove gets past "not from Scan" and its other
// checks are what must refuse. Only tests can make one: outside them, a
// Candidate Remove accepts has been through all of Scan's checks.
func at(t *testing.T, path string) Candidate {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return Candidate{Path: path, fi: fi}
}

func open(t *testing.T, root string) *os.Root {
	t.Helper()
	r, err := Open(root)
	must(t, err)
	t.Cleanup(func() { r.Close() })
	return r
}

// A parent swapped for a symlink after the last Vet but before the delete:
// the walk down refuses it, so nothing is deleted, inside DIR or out.
func TestRemoveRaceCantLeaveRoot(t *testing.T) { removeRaceCantLeaveRoot(t, newTrash(t)) }

func removeRaceCantLeaveRoot(t *testing.T, trash string) {
	for name, target := range map[string]func(tmp string) string{
		"absolute symlink": func(tmp string) string { return filepath.Join(tmp, "outside") },
		"relative symlink": func(string) string { return "../outside" },
	} {
		t.Run(name, func(t *testing.T) {
			tmp, root := build(t, "root/a/node_modules/f", "outside/node_modules/precious")
			tg := DefaultTargets()
			r := open(t, root)
			cands := scanned(root, tg, 4)

			beforeRemove = func(string) {
				a := filepath.Join(root, "a")
				must(t, os.RemoveAll(a))
				must(t, os.Symlink(target(tmp), a))
			}
			t.Cleanup(func() { beforeRemove = func(string) {} })

			if deleted, errs := Remove(r, tg, cands, 4, trash); len(deleted) != 0 {
				t.Errorf("deleted %v (errs %v), want nothing", deleted, errs)
			}
			if !exists(filepath.Join(tmp, "outside/node_modules/precious")) {
				t.Error("deleted outside DIR")
			}
		})
	}
}

// Swaps inside DIR between Vet and the delete can't redirect it to another
// project's folder: not a symlink (a -> b), and not a rename that puts b in
// a's place, where every step is a real folder. Only the folder the scan saw
// is ever deleted.
func TestRemoveSwapCantRedirectInsideRoot(t *testing.T) {
	removeSwapCantRedirectInsideRoot(t, newTrash(t))
}

func removeSwapCantRedirectInsideRoot(t *testing.T, trash string) {
	for name, c := range map[string]struct {
		swap     func(root string)
		precious string // where b's file is after the swap
	}{
		"symlink": {func(root string) {
			a := filepath.Join(root, "a")
			must(t, os.Rename(a, a+"-moved"))
			must(t, os.Symlink("b", a))
		}, "b/node_modules/precious"},
		"rename": {func(root string) {
			a := filepath.Join(root, "a")
			must(t, os.Rename(a, a+"-moved"))
			must(t, os.Rename(filepath.Join(root, "b"), a))
		}, "a/node_modules/precious"},
		"target renamed": {func(root string) {
			nm := filepath.Join(root, "a/node_modules")
			must(t, os.Rename(nm, nm+"-moved"))
			must(t, os.Rename(filepath.Join(root, "b/node_modules"), nm))
		}, "a/node_modules/precious"},
	} {
		t.Run(name, func(t *testing.T) {
			_, root := build(t, "root/a/node_modules/f", "root/b/node_modules/precious", "root/b/.sweepignore")
			tg := DefaultTargets()
			r := open(t, root)
			cands := scanned(root, tg, 4)
			if len(cands) != 1 {
				t.Fatalf("listed %v, want only a/node_modules (b is opted out)", cands)
			}
			beforeRemove = func(string) { c.swap(root) }
			t.Cleanup(func() { beforeRemove = func(string) {} })

			Remove(r, tg, cands, 4, trash)
			if !exists(filepath.Join(root, c.precious)) {
				t.Errorf("b's opted-out node_modules was deleted (%s gone)", c.precious)
			}
		})
	}
}

// Open refuses a root that is no longer the folder that was checked.
func TestOpenRefusesSwappedRoot(t *testing.T) {
	tmp, root := build(t, "other/")
	must(t, os.Remove(root))
	must(t, os.Symlink(filepath.Join(tmp, "other"), root))
	if r, err := Open(root); err == nil {
		r.Close()
		t.Error("Open followed a root swapped for a symlink")
	}
}

// A .sweepignore added after the scan still protects the folder.
func TestRemoveHonorsIgnoreAddedAfterScan(t *testing.T) {
	removeHonorsIgnoreAddedAfterScan(t, newTrash(t))
}

func removeHonorsIgnoreAddedAfterScan(t *testing.T, trash string) {
	_, root := build(t, "root/a/node_modules/f", "root/b/node_modules/f")
	tg := DefaultTargets()
	cands := scanned(root, tg, 4)
	must(t, os.WriteFile(filepath.Join(root, "a", IgnoreFile), nil, 0o644))

	deleted, _ := Remove(open(t, root), tg, cands, 4, trash)
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0].Path, "b/node_modules") {
		t.Errorf("deleted %v, want only b/node_modules", deleted)
	}
	if !exists(filepath.Join(root, "a/node_modules/f")) {
		t.Error("deleted a folder marked after the scan")
	}
}

// On another disk, the Trash is that disk's .Trashes/<uid>, like Finder's,
// so the move stays one rename.
func TestFindTrashOnAnotherVolume(t *testing.T) {
	_, root := build(t, "root/")
	vol := filepath.Join(root, "vol")
	mount(t, vol)
	t.Setenv("HOME", newTrash(t)) // on the boot disk, so not vol's Trash
	code := filepath.Join(vol, "code")
	must(t, os.MkdirAll(filepath.Join(code, "web/node_modules/pkg"), 0o755))

	trash, err := FindTrash(code)
	if want := filepath.Join(vol, ".Trashes", strconv.Itoa(os.Getuid())); err != nil || trash != want {
		t.Fatalf("FindTrash = %q, %v; want %q", trash, err, want)
	}
	if fi, err := os.Lstat(filepath.Dir(trash)); err != nil || fi.Mode()&os.ModeSticky == 0 {
		t.Errorf(".Trashes is %v, %v; want sticky", fi, err)
	}
	tg := DefaultTargets()
	moved, errs := Remove(open(t, code), tg, scanned(code, tg, 4), 4, trash)
	if len(moved) != 1 || len(errs) != 0 || !exists(filepath.Join(trash, "code › web › node_modules/pkg")) {
		t.Errorf("moved %v, errs %v, Trash holds %q", moved, errs, inTrash(t, trash))
	}
}
