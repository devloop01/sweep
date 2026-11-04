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
	// Remove re-checks too: even if asked, it won't delete them.
	deleted, _ := Remove(open(t, root), DefaultTargets(), []Candidate{{Path: filepath.Join(root, "lib/build")}}, 4)
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
