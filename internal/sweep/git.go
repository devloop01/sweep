//go:build darwin && arm64

package sweep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// gitOnly are target names that some projects use for real files (scripts
// in build/, a committed or downloaded dist/). They're deleted only when git
// confirms nothing in them is tracked, so outside a git repo they're kept.
var gitOnly = []string{"dist", "build", "out"}

// keep reports whether dir must be kept although it's a target, because it
// may hold real files. Inside a git repo, a folder git tracks files in is
// kept, and so is one git can't answer for. Outside a repo, only gitOnly
// names are kept, and git isn't run.
func keep(dir string) bool {
	if !inRepo(dir) {
		return slices.Contains(gitOnly, filepath.Base(dir))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// :(literal) so a folder name is never read as a git pattern.
	cmd := exec.CommandContext(ctx, "git", "-C", filepath.Dir(dir), "ls-files", "-z", "--", ":(literal)"+filepath.Base(dir))
	out, err := cmd.Output()
	// ponytail: without Xcode's command line tools, /usr/bin/git fails (and
	// may offer to install them); every folder in a repo is then kept.
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
