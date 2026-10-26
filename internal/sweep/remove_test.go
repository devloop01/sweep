//go:build darwin && arm64

package sweep

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRemove(t *testing.T) {
	for _, w := range []int{1, 4} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/workers=%d", tc.name, w), func(t *testing.T) {
				tmp, root := build(t, tc.tree...)
				tg := DefaultLists().Targets(tc.build, tc.deploy)
				cands := Scan(root, tg, w)
				deleted, errs := Remove(open(t, root), tg, cands, w)
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
			})
		}
	}
}

// A candidate swapped for a symlink between scan and delete is not deleted.
func TestRemoveRevetsSymlinkedCandidate(t *testing.T) {
	tmp, root := build(t, "root/a/node_modules/f", "outside/precious/file")
	tg := DefaultLists().Targets(false, false)
	cands := Scan(root, tg, 4)

	nm := filepath.Join(root, "a/node_modules")
	must(t, os.RemoveAll(nm))
	must(t, os.Symlink(filepath.Join(tmp, "outside/precious"), nm))

	deleted, errs := Remove(open(t, root), tg, cands, 4)
	if len(deleted) != 0 || len(errs) != 0 {
		t.Errorf("deleted %v, errs %v", deleted, errs)
	}
	if !exists(nm) || !exists(filepath.Join(tmp, "outside/precious/file")) {
		t.Error("symlink or its target was deleted")
	}
}

// A parent swapped for a symlink must not lead the delete outside DIR.
func TestRemoveRevetsSymlinkedParent(t *testing.T) {
	tmp, root := build(t, "root/a/node_modules/f", "outside/node_modules/precious")
	tg := DefaultLists().Targets(false, false)
	cands := Scan(root, tg, 4)

	a := filepath.Join(root, "a")
	must(t, os.RemoveAll(a))
	must(t, os.Symlink(filepath.Join(tmp, "outside"), a))

	deleted, _ := Remove(open(t, root), tg, cands, 4)
	if len(deleted) != 0 || !exists(filepath.Join(tmp, "outside/node_modules/precious")) {
		t.Errorf("deleted outside DIR: %v", deleted)
	}
}

// Candidates that were never vetted against this root are refused.
func TestRemoveRefusesOutsideRoot(t *testing.T) {
	tmp, root := build(t, "outside/node_modules/precious", "root/x/")
	out := filepath.Join(tmp, "outside/node_modules")
	deleted, _ := Remove(open(t, root), DefaultLists().Targets(false, false), []Candidate{{Path: out}, {Path: root}}, 4)
	if len(deleted) != 0 || !exists(out) || !exists(root) {
		t.Errorf("deleted %v", deleted)
	}
}

func TestRemoveReportsFailures(t *testing.T) {
	_, root := build(t, "root/a/node_modules/pkg/f", "root/b/node_modules/f")
	locked := filepath.Join(root, "a/node_modules/pkg")
	must(t, os.Chmod(locked, 0o500))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	tg := DefaultLists().Targets(false, false)
	deleted, errs := Remove(open(t, root), tg, Scan(root, tg, 4), 4)
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0].Path, "b/node_modules") {
		t.Errorf("deleted %v, want only b/node_modules", deleted)
	}
	if len(errs) != 1 {
		t.Errorf("errs %v, want 1", errs)
	}
	if !exists(filepath.Join(locked, "f")) {
		t.Error("locked file was deleted")
	}
}

func open(t *testing.T, root string) *os.Root {
	t.Helper()
	r, err := Open(root)
	must(t, err)
	t.Cleanup(func() { r.Close() })
	return r
}

// The race Vet can't close: a parent swapped for a symlink after the last
// Vet but before the delete. Deleting through os.Root must not follow it out.
func TestRemoveRaceCantLeaveRoot(t *testing.T) {
	for name, target := range map[string]func(tmp string) string{
		"absolute symlink": func(tmp string) string { return filepath.Join(tmp, "outside") },
		"relative symlink": func(string) string { return "../outside" },
	} {
		t.Run(name, func(t *testing.T) {
			tmp, root := build(t, "root/a/node_modules/f", "outside/node_modules/precious")
			tg := DefaultLists().Targets(false, false)
			r := open(t, root)
			cands := Scan(root, tg, 4)

			beforeRemove = func(string) {
				a := filepath.Join(root, "a")
				must(t, os.RemoveAll(a))
				must(t, os.Symlink(target(tmp), a))
			}
			t.Cleanup(func() { beforeRemove = func(string) {} })

			deleted, errs := Remove(r, tg, cands, 4)
			if len(deleted) != 0 || len(errs) != 1 {
				t.Errorf("deleted %v, errs %v; want nothing deleted and one error", deleted, errs)
			}
			if !exists(filepath.Join(tmp, "outside/node_modules/precious")) {
				t.Error("deleted outside DIR")
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
	_, root := build(t, "root/a/node_modules/f", "root/b/node_modules/f")
	tg := DefaultLists().Targets(false, false)
	cands := Scan(root, tg, 4)
	must(t, os.WriteFile(filepath.Join(root, "a", IgnoreFile), nil, 0o644))

	deleted, _ := Remove(open(t, root), tg, cands, 4)
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0].Path, "b/node_modules") {
		t.Errorf("deleted %v, want only b/node_modules", deleted)
	}
	if !exists(filepath.Join(root, "a/node_modules/f")) {
		t.Error("deleted a folder marked after the scan")
	}
}
