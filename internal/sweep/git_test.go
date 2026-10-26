//go:build darwin && arm64

package sweep

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// gitRepo makes dir a git repo and stages paths (relative to dir), which is
// enough for git to track them.
func gitRepo(t *testing.T, dir string, paths ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, args := range [][]string{{"init", "-q"}, append([]string{"add", "--"}, paths...)} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// A target with files git tracks is kept and reported; untracked or ignored
// build output in the same repo is deleted.
func TestGitTrackedTargetsAreKept(t *testing.T) {
	tmp, root := build(t, "root/proj/build/tool.sh", "root/proj/dist/app.js", "root/proj/node_modules/x/f",
		"root/proj/out/", "root/proj/.gitignore")
	proj := filepath.Join(root, "proj")
	must(t, os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("dist/\nnode_modules/\n"), 0o644))
	gitRepo(t, proj, "build/tool.sh", ".gitignore")

	tg := DefaultTargets()
	cands, kept := Scan(root, tg, 4)
	if got := rels(t, tmp, cands); !slices.Equal(got, []string{"root/proj/dist", "root/proj/node_modules", "root/proj/out"}) {
		t.Errorf("listed %q", got)
	}
	if !slices.Equal(kept, []string{filepath.Join(proj, "build")}) {
		t.Errorf("kept %q, want proj/build", kept)
	}
	Remove(open(t, root), tg, cands, 4)
	if !exists(filepath.Join(proj, "build/tool.sh")) || exists(filepath.Join(proj, "dist")) {
		t.Error("wrong folders deleted")
	}
}

// Files that become tracked after the scan still protect the folder.
func TestRemoveKeepsFolderTrackedAfterScan(t *testing.T) {
	_, root := build(t, "root/proj/dist/app.js", "root/proj/README")
	proj := filepath.Join(root, "proj")
	gitRepo(t, proj, "README")
	tg := DefaultTargets()
	cands := scanned(root, tg, 4)
	if len(cands) != 1 {
		t.Fatalf("listed %v, want dist", cands)
	}
	gitRepo(t, proj, "dist/app.js")

	if deleted, _ := Remove(open(t, root), tg, cands, 4); len(deleted) != 0 || !exists(filepath.Join(proj, "dist/app.js")) {
		t.Errorf("deleted %v, a folder git now tracks", deleted)
	}
}

// When git can't answer, folders inside a repo are kept; folders outside any
// repo don't need git at all.
func TestWithoutGitFoldersInRepoAreKept(t *testing.T) {
	_, root := build(t, "root/proj/node_modules/f", "root/proj/README", "root/plain/node_modules/f")
	gitRepo(t, filepath.Join(root, "proj"), "README")
	t.Setenv("PATH", t.TempDir()) // no git on PATH

	cands, kept := Scan(root, DefaultTargets(), 4)
	if len(cands) != 1 || cands[0].Path != filepath.Join(root, "plain/node_modules") {
		t.Errorf("listed %v, want only plain/node_modules", cands)
	}
	if !slices.Equal(kept, []string{filepath.Join(root, "proj/node_modules")}) {
		t.Errorf("kept %q, want proj/node_modules", kept)
	}
}
