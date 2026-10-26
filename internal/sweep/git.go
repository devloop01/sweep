//go:build darwin && arm64

package sweep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// tracked reports whether git tracks any file inside dir, so the folder
// isn't disposable output (a committed dist/, scripts kept in build/). A
// folder outside any git repo has nothing tracked and git isn't run. Inside
// one, if git can't answer, the folder counts as tracked and is kept.
func tracked(dir string) bool {
	if !inRepo(dir) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// :(literal) so a folder name is never read as a git pattern.
	cmd := exec.CommandContext(ctx, "git", "-C", filepath.Dir(dir), "ls-files", "-z", "--", ":(literal)"+filepath.Base(dir))
	out, err := cmd.Output()
	// ponytail: without Xcode's command line tools, /usr/bin/git fails (and
	// may offer to install them); every folder inside a repo is then kept.
	return err != nil || len(out) > 0
}

// inRepo reports whether a folder above dir has a .git entry.
func inRepo(dir string) bool {
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}
