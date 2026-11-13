//go:build darwin && arm64

package sweep

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitRepo makes dir a git repo and stages paths (relative to dir), which is
// enough for git to track them.
func gitRepo(t *testing.T, dir string, paths ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	add := []string{"add"}
	for len(paths) > 0 && strings.HasPrefix(paths[0], "-") { // flags, like -f
		add, paths = append(add, paths[0]), paths[1:]
	}
	for _, args := range [][]string{{"init", "-q"}, append(append(add, "--"), paths...)} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// keptPaths is kept, relative to root.
func keptPaths(t *testing.T, root string, kept []string) []string {
	var out []string
	for _, k := range kept {
		r, err := filepath.Rel(root, k)
		must(t, err)
		out = append(out, r)
	}
	return out
}

// In a repo: a target with files git tracks is kept, and so is build output
// git doesn't ignore (out/ here: it may hold new, uncommitted files).
// Ignored build output and untracked caches are deleted.
func TestGitTrackedTargetsAreKept(t *testing.T) {
	tmp, root := build(t, "root/proj/build/tool.sh", "root/proj/dist/app.js", "root/proj/node_modules/x/f",
		"root/proj/out/", "root/proj/target/debug/app", "root/proj/.gitignore")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("dist/\nnode_modules/\ntarget/\n"), 0o644))
	gitRepo(t, proj, "build/tool.sh", ".gitignore")

	tg := DefaultTargets()
	cands, kept := Scan(root, tg, 4, nil)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"root/proj/dist", "root/proj/node_modules", "root/proj/target"}) {
		t.Errorf("listed %q", got)
	}
	if got := keptPaths(t, root, kept); !slices.Equal(got, []string{"proj/build", "proj/out"}) {
		t.Errorf("kept %q, want proj/build and proj/out", got)
	}
	Remove(open(t, root), tg, cands, 4)
	if !exists(filepath.Join(proj, "build/tool.sh")) || exists(filepath.Join(proj, "dist")) {
		t.Error("wrong folders deleted")
	}
}

// Outside any repo, dist/build/out are kept (sweep can't tell they're only
// output), while caches like node_modules and .next are still deleted.
func TestBuildFoldersOutsideGitAreKept(t *testing.T) {
	tmp, root := build(t, "root/lib/build/Gruntfile.js", "root/lib/dist/x.js", "root/lib/out/",
		"root/lib/target/notes.txt", "root/lib/node_modules/f", "root/lib/.next/f")
	cands, kept := Scan(root, DefaultTargets(), 4, nil)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"root/lib/.next", "root/lib/node_modules"}) {
		t.Errorf("listed %q, want only the caches", got)
	}
	if got := keptPaths(t, root, kept); !slices.Equal(got, []string{"lib/build", "lib/dist", "lib/out", "lib/target"}) {
		t.Errorf("kept %q", got)
	}
	// Remove re-checks too: even if asked, it won't delete them. Only Vet
	// stands in the way here: the folder is real, in root, and is the one
	// the candidate names.
	deleted, _ := Remove(open(t, root), DefaultTargets(), []Candidate{at(t, filepath.Join(root, "lib/build"))}, 4)
	if len(deleted) != 0 || !exists(filepath.Join(root, "lib/build/Gruntfile.js")) {
		t.Error("deleted a build folder outside git")
	}
}

// !dist in the config means dist is never a target, even when git would
// allow it.
func TestUserCanRemoveBuildName(t *testing.T) {
	tmp, root := build(t, "root/proj/dist/a.js", "root/proj/build/b.js", "root/proj/README", "root/proj/.gitignore")
	must(t, os.WriteFile(filepath.Join(root, "proj/.gitignore"), []byte("dist/\nbuild/\n"), 0o644))
	gitRepo(t, filepath.Join(root, "proj"), "README", ".gitignore")
	cands, kept := Scan(root, targetsFor(t, "!dist\n"), 4, nil)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"root/proj/build"}) || len(kept) != 0 {
		t.Errorf("listed %q, kept %+v; want only build", got, kept)
	}
}

// Files that become tracked after the scan still protect the folder.
func TestRemoveKeepsFolderTrackedAfterScan(t *testing.T) {
	_, root := build(t, "root/proj/dist/app.js", "root/proj/README", "root/proj/.gitignore")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("dist/\n"), 0o644))
	gitRepo(t, proj, "README", ".gitignore")
	tg := DefaultTargets()
	cands := scanned(root, tg, 4)
	if len(cands) != 1 {
		t.Fatalf("listed %v, want dist", cands)
	}
	gitRepo(t, proj, "-f", "dist/app.js")

	if deleted, _ := Remove(open(t, root), tg, cands, 4); len(deleted) != 0 || !exists(filepath.Join(proj, "dist/app.js")) {
		t.Errorf("deleted %v, a folder git now tracks", deleted)
	}
}

// Build output in a repo is deleted only when an ignore rule names it: not
// when it's untracked but not ignored (new files), not when only a catch-all
// * covers it (a repo around the whole home folder), and not for a ! rule.
// Named rules count wherever they are: a nested .gitignore, or
// .git/info/exclude.
func TestBuildFoldersNeedANamedIgnoreRule(t *testing.T) {
	tmp, _ := build(t, "home/.gitignore",
		"home/code/loose/build/new.sh", "home/code/loose/node_modules/f",
		"home/code/proj/README", "home/code/proj/.gitignore", "home/code/proj/build/new.sh",
		"home/code/proj/apps/web/.gitignore", "home/code/proj/apps/web/dist/x.js",
		"home/code/proj/out/x", "home/code/proj/target/x")
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "code")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(home, ".gitignore"), []byte("*\n"), 0o644))
	gitRepo(t, home, "-f", ".gitignore")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("target/\n!target/\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(proj, "apps/web/.gitignore"), []byte("dist\n"), 0o644))
	gitRepo(t, proj, "README", ".gitignore", "apps/web/.gitignore")
	must(t, os.WriteFile(filepath.Join(proj, ".git/info/exclude"), []byte("out/\n"), 0o644))

	cands, kept := Scan(root, DefaultTargets(), 4, nil)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"home/code/loose/node_modules", "home/code/proj/apps/web/dist", "home/code/proj/out"}) {
		t.Errorf("listed %q", got)
	}
	if got := keptPaths(t, root, kept); !slices.Equal(got, []string{"loose/build", "proj/build", "proj/target"}) {
		t.Errorf("kept %q, want loose/build (only * covers it), proj/build (not ignored), proj/target (a ! rule)", got)
	}
}

// When git can't answer, folders inside a repo are kept; caches outside any
// repo don't need git at all.
func TestWithoutGitFoldersInRepoAreKept(t *testing.T) {
	_, root := build(t, "root/proj/node_modules/f", "root/proj/README", "root/plain/node_modules/f")
	gitRepo(t, filepath.Join(root, "proj"), "README")
	t.Setenv("PATH", t.TempDir()) // no git on PATH

	cands, kept := Scan(root, DefaultTargets(), 4, nil)
	if len(cands) != 1 || cands[0].Path != filepath.Join(root, "plain/node_modules") {
		t.Errorf("listed %v, want only plain/node_modules", cands)
	}
	if !slices.Equal(kept, []string{filepath.Join(root, "proj/node_modules")}) {
		t.Errorf("kept %q, want proj/node_modules", kept)
	}
}

// Git sets GIT_DIR and GIT_INDEX_FILE for hooks and rebase --exec, and some
// shells export them for a dotfiles repo. sweep still asks the repo each
// folder is in, not that one.
func TestGitEnvironmentDoesNotRedirect(t *testing.T) {
	tmp, root := build(t, "other/README", "root/proj/node_modules/pkg/f", "root/proj/.output/f",
		"root/proj/dist/app.js", "root/proj/.gitignore")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("dist/\n"), 0o644))
	gitRepo(t, proj, "node_modules/pkg/f", ".gitignore")
	other := filepath.Join(tmp, "other")
	gitRepo(t, other, "README")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git/index"))

	tg := DefaultTargets()
	cands, kept := Scan(root, tg, 4, nil)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"root/proj/.output", "root/proj/dist"}) {
		t.Errorf("listed %q, want .output and dist", got)
	}
	if got := keptPaths(t, root, kept); !slices.Equal(got, []string{"proj/node_modules"}) {
		t.Errorf("kept %q, want the tracked proj/node_modules", got)
	}
	if Vet(root, filepath.Join(proj, "node_modules"), tg) {
		t.Error("Vet allowed a folder with tracked files")
	}
}

// A target with its own .git is a checkout of its own (a nested clone, a
// worktree), whatever the repo around it ignores, and whether .git is a
// folder or a worktree's file.
func TestNestedCheckoutIsKept(t *testing.T) {
	tmp, root := build(t, "root/proj/README", "root/proj/.gitignore", "root/proj/build/main.go",
		"root/proj/out/main.go", "root/proj/dist/app.js", "root/plain/node_modules/.git/HEAD")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("build/\nout/\ndist/\n"), 0o644))
	gitRepo(t, proj, "README", ".gitignore")
	gitRepo(t, filepath.Join(proj, "build"), "main.go")
	must(t, os.WriteFile(filepath.Join(proj, "out/.git"), []byte("gitdir: ../.git/worktrees/out\n"), 0o644))

	tg := DefaultTargets()
	cands, kept := Scan(root, tg, 4, nil)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"root/proj/dist"}) {
		t.Errorf("listed %q, want only dist", got)
	}
	if got := keptPaths(t, root, kept); !slices.Equal(got, []string{"plain/node_modules", "proj/build", "proj/out"}) {
		t.Errorf("kept %q", got)
	}
	// A .git that appears after the scan protects the folder too.
	must(t, os.WriteFile(filepath.Join(proj, "dist/.git"), []byte("gitdir: elsewhere\n"), 0o644))
	if deleted, _ := Remove(open(t, root), tg, cands, 4); len(deleted) != 0 || !exists(filepath.Join(proj, "dist/app.js")) {
		t.Errorf("deleted %v, a folder with its own .git", deleted)
	}
}

// Targets in one folder are put to git together; each still gets its own
// answer, the same one it gets alone.
func TestKeepAllAnswersEachFolder(t *testing.T) {
	_, root := build(t, "root/proj/README", "root/proj/.gitignore", "root/proj/node_modules/f",
		"root/proj/.next/f", "root/proj/.turbo/tracked", "root/proj/dist/f", "root/proj/build/f",
		"root/proj/out/tracked", "root/proj/target/f")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("dist/\nout/\n/target\n!/target\n"), 0o644))
	gitRepo(t, proj, "-f", "README", ".gitignore", ".turbo/tracked", "out/tracked")

	names := []string{"node_modules", ".next", ".turbo", "dist", "build", "out", "target"}
	want := []bool{false, false, true, false, true, true, true}
	var dirs []string
	for _, n := range names {
		dirs = append(dirs, filepath.Join(proj, n))
	}
	got := keepAll(dirs)
	for i, n := range names {
		if got[i] != want[i] {
			t.Errorf("keepAll: %s kept %v, want %v", n, got[i], want[i])
		}
		if alone := keep(dirs[i]); alone != want[i] {
			t.Errorf("keep(%s) = %v, want %v", n, alone, want[i])
		}
	}
	if len(keepAll(nil)) != 0 {
		t.Error("keepAll(nil) isn't empty")
	}
}
