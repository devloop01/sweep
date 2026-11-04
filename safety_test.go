//go:build darwin && arm64

package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devloop01/sweep/internal/sweep"
)

// nasty builds a projects folder full of traps next to a folder outside it,
// and returns both. Only real, untracked target folders may ever go.
func nasty(t *testing.T) (root, outside string) {
	t.Helper()
	top, _ := filepath.EvalSymlinks(t.TempDir())
	root, outside = filepath.Join(top, "code"), filepath.Join(top, "outside")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(filepath.Join(outside, "important/notes.txt"), "keep me")
	write(filepath.Join(outside, "node_modules/x"), "outside root")

	in := func(p string) string { return filepath.Join(root, p) }
	// A Turborepo: its folders go, its source stays.
	write(in("mono/turbo.json"), "{}")
	write(in("mono/package.json"), "{}")
	write(in("mono/node_modules/a/index.js"), "a")
	write(in("mono/apps/web/package.json"), "{}")
	write(in("mono/apps/web/src/page.tsx"), "page")
	write(in("mono/apps/web/.next/cache/b"), "b")
	write(in("mono/packages/ui/.turbo/c"), "c")
	// node_modules that is a symlink out of root, and one to a sibling.
	write(in("linked/package.json"), "{}")
	link(filepath.Join(outside, "important"), in("linked/node_modules"))
	link("../mono/apps/web/src", in("linked2/node_modules"))
	// A real node_modules holding links out: only the links may go.
	write(in("links/node_modules/real.js"), "r")
	link(filepath.Join(outside, "important"), in("links/node_modules/escape"))
	link(filepath.Join(outside, "important/notes.txt"), in("links/node_modules/file"))
	// A project folder that is itself a symlink out of root.
	link(outside, in("aliased"))
	// Nested node_modules: the outer one is the target.
	write(in("nested/node_modules/pkg/node_modules/dep/x"), "x")
	// In git: tracked dist stays, ignored dist and target go.
	write(in("repo/dist/app.js"), "tracked")
	write(in("repo/src/main.rs"), "fn main() {}")
	write(in("repo/target/debug/bin"), "built")
	write(in("repo/.gitignore"), "target/\n")
	git(in("repo"), "init", "-q")
	git(in("repo"), "add", "-A")
	git(in("repo"), "commit", "-qm", "x")
	write(in("repo2/dist/out.js"), "built")
	write(in("repo2/.gitignore"), "dist/\n")
	git(in("repo2"), "init", "-q")
	git(in("repo2"), "add", "-A")
	git(in("repo2"), "commit", "-qm", "x")
	// In git but not ignored: new files nobody has committed yet.
	write(in("wip/README"), "readme")
	write(in("wip/build/deploy.sh"), "uncommitted work")
	git(in("wip"), "init", "-q")
	git(in("wip"), "add", "README")
	git(in("wip"), "commit", "-qm", "x")
	// Outside git, build/target may be real files.
	write(in("loose/build/tool.sh"), "real")
	write(in("loose/target/notes.md"), "real")
	// Opted out, inside .git, inside an app, and a file named like a target.
	write(in("optout/.sweepignore"), "")
	write(in("optout/node_modules/x"), "x")
	write(in("withgit/.git/node_modules/x"), "x")
	write(in("Tool.app/Contents/node_modules/x"), "x")
	write(in("file/node_modules"), "I'm a file")
	// Odd names.
	write(in("we ird\nna'me/node_modules/x"), "x")
	write(in("ünï/.svelte-kit/x"), "x")
	return root, outside
}

// snapshot records every entry under dirs without following links: its
// kind, and its contents or link target.
func snapshot(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, d := range dirs {
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			switch {
			case e.Type()&fs.ModeSymlink != 0:
				l, _ := os.Readlink(p)
				m[p] = "link " + l
			case e.IsDir():
				m[p] = "dir"
			default:
				b, _ := os.ReadFile(p)
				m[p] = fmt.Sprintf("file %x", sha256.Sum256(b))
			}
			return nil
		})
	}
	return m
}

// Random use of the screen (keys and clicks, projects on and off, sorting,
// folding) then real deletes, many times over. Every deletion must be a
// folder the user picked at that moment (selected, or the highlighted row's
// with nothing selected), and every other file, in and outside the folder,
// must be byte-for-byte unchanged.
func TestRandomUseDeletesOnlyWhatWasPicked(t *testing.T) {
	keys := []string{"up", "down", "down", "pgdn", "home", "end", "space", "space", "all",
		"tree", "sort", "fold", "left", "right", "next", "prev", "help",
		"enter", "delete", "delete", "enter", "yes", "yes", "no", "left", "right", "click", "click", "click"}
	total := 0
	defer func() {
		if total < 100 {
			t.Errorf("only %d folders deleted over all runs; the test isn't exercising deletes", total)
		}
		t.Logf("%d folders deleted over all runs", total)
	}()
	for seed := range uint64(60) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 1))
			root, outside := nasty(t)
			before := snapshot(t, filepath.Dir(root))
			tg := sweep.DefaultTargets()
			cands, kept := sweep.Scan(root, tg, 4, nil)
			r, err := sweep.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			s := &screen{}
			var finder sweep.Finder
			for _, c := range cands {
				rel, _ := filepath.Rel(root, c.Path)
				s.addIn(c, printable(rel), projectOf(&finder, root, c.Path))
			}
			s.finish(cands, len(kept))
			var deleted []string
			for range 40 {
				k := keys[rng.IntN(len(keys))]
				// What the user picked, worked out here, not by the screen.
				var picked []string
				if s.ask == nil && !s.helping && len(s.rows) > 0 {
					for _, it := range s.items {
						if it.selected && it.state == ready {
							picked = append(picked, it.cand.Path)
						}
					}
					if len(picked) == 0 {
						r := s.rows[s.cursor]
						for _, it := range s.items[r.i : r.i+r.n] {
							if it.state == ready {
								picked = append(picked, it.cand.Path)
							}
						}
					}
				}
				asked := slices.Clone(s.ask)
				var del []sweep.Candidate
				if k == "click" {
					s.view(look{}, 80, 24)
					del = s.click(rng.IntN(80), rng.IntN(24))
				} else {
					del = s.key(k)
				}
				if k == "delete" && asked == nil && s.ask != nil {
					got, want := slices.Sorted(slices.Values(s.ask)), slices.Sorted(slices.Values(picked))
					if !slices.Equal(got, want) {
						t.Fatalf("d asked about %q, but the user picked %q", got, want)
					}
				}
				for _, c := range del {
					if !slices.Contains(asked, c.Path) {
						t.Fatalf("deleting %s, which the dialog didn't ask about (%q)", c.Path, asked)
					}
				}
				done, errs := sweep.Remove(r, tg, del, 4)
				if len(errs) > 0 {
					t.Fatalf("delete failed: %v", errs)
				}
				for _, c := range del {
					s.done(c.Path, nil)
				}
				for _, c := range done {
					deleted = append(deleted, c.Path)
				}
				total += len(done)
			}

			// Only the deleted folders' own contents may be missing; nothing
			// else, anywhere, may change.
			after := snapshot(t, filepath.Dir(root))
			under := func(p string) bool {
				return slices.ContainsFunc(deleted, func(d string) bool { return p == d || strings.HasPrefix(p, d+"/") })
			}
			for p, was := range before {
				now, ok := after[p]
				switch {
				case under(p) && ok:
					t.Errorf("%s survived its folder's delete", p)
				case !under(p) && !ok:
					t.Errorf("%s was deleted, but the user didn't pick it", p)
				case !under(p) && now != was:
					t.Errorf("%s changed: %s -> %s", p, was, now)
				}
			}
			for p := range after {
				if _, ok := before[p]; !ok {
					t.Errorf("%s appeared", p)
				}
			}
			for _, d := range deleted {
				if strings.HasPrefix(d, outside) || !slices.ContainsFunc(cands, func(c sweep.Candidate) bool { return c.Path == d }) {
					t.Errorf("deleted %s, which the scan never listed", d)
				}
			}
		})
	}
}

// The scan lists exactly the safe folders in the nasty tree, and Remove
// refuses every trap even when asked directly.
func TestNastyTreeListsOnlySafeFolders(t *testing.T) {
	root, outside := nasty(t)
	tg := sweep.DefaultTargets()
	cands, _ := sweep.Scan(root, tg, 4, nil)
	var got []string
	for _, c := range cands {
		rel, _ := filepath.Rel(root, c.Path)
		got = append(got, rel)
	}
	slices.Sort(got)
	want := []string{"links/node_modules", "mono/apps/web/.next", "mono/node_modules", "mono/packages/ui/.turbo",
		"nested/node_modules", "repo/target", "repo2/dist", "we ird\nna'me/node_modules", "ünï/.svelte-kit"}
	if !slices.Equal(got, want) {
		t.Errorf("listed %q\nwant   %q", got, want)
	}

	before := snapshot(t, filepath.Dir(root))
	r, _ := sweep.Open(root)
	defer r.Close()
	var traps []sweep.Candidate
	for _, p := range []string{"linked/node_modules", "linked2/node_modules", "aliased/node_modules", "repo/dist",
		"loose/build", "loose/target", "wip/build", "optout/node_modules", "withgit/.git/node_modules",
		"Tool.app/Contents/node_modules", "file/node_modules", "mono/apps/web/src", "."} {
		traps = append(traps, sweep.Candidate{Path: filepath.Join(root, p)})
	}
	traps = append(traps, sweep.Candidate{Path: filepath.Join(outside, "node_modules")}, sweep.Candidate{Path: outside})
	if done, errs := sweep.Remove(r, tg, traps, 4); len(done) != 0 || len(errs) != 0 {
		t.Errorf("Remove deleted %v (errors %v)", done, errs)
	}
	if after := snapshot(t, filepath.Dir(root)); len(after) != len(before) {
		t.Errorf("the tree changed: %d entries -> %d", len(before), len(after))
	}
}
