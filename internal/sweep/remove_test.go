//go:build darwin

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
				tg := NewTargets(tc.build, tc.deploy)
				cands := Scan(root, tg, w)
				deleted, errs := Remove(root, tg, cands, w)
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
	tg := NewTargets(false, false)
	cands := Scan(root, tg, 4)

	nm := filepath.Join(root, "a/node_modules")
	must(t, os.RemoveAll(nm))
	must(t, os.Symlink(filepath.Join(tmp, "outside/precious"), nm))

	deleted, errs := Remove(root, tg, cands, 4)
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
	tg := NewTargets(false, false)
	cands := Scan(root, tg, 4)

	a := filepath.Join(root, "a")
	must(t, os.RemoveAll(a))
	must(t, os.Symlink(filepath.Join(tmp, "outside"), a))

	deleted, _ := Remove(root, tg, cands, 4)
	if len(deleted) != 0 || !exists(filepath.Join(tmp, "outside/node_modules/precious")) {
		t.Errorf("deleted outside DIR: %v", deleted)
	}
}

// Candidates that were never vetted against this root are refused.
func TestRemoveRefusesOutsideRoot(t *testing.T) {
	tmp, root := build(t, "outside/node_modules/precious", "root/x/")
	out := filepath.Join(tmp, "outside/node_modules")
	deleted, _ := Remove(root, NewTargets(false, false), []Candidate{{Path: out}, {Path: root}}, 4)
	if len(deleted) != 0 || !exists(out) || !exists(root) {
		t.Errorf("deleted %v", deleted)
	}
}

func TestRemoveReportsFailures(t *testing.T) {
	_, root := build(t, "root/a/node_modules/pkg/f", "root/b/node_modules/f")
	locked := filepath.Join(root, "a/node_modules/pkg")
	must(t, os.Chmod(locked, 0o500))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	tg := NewTargets(false, false)
	deleted, errs := Remove(root, tg, Scan(root, tg, 4), 4)
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
