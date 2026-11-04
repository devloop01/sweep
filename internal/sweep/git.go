//go:build darwin && arm64

package sweep

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// gitOnly are target names that some projects use for real files (scripts
// in build/, a committed or downloaded dist/, anything called target/).
// They're deleted only in a git repo whose ignore rules name them, with
// nothing in them tracked; outside a repo they're kept.
var gitOnly = []string{"dist", "build", "out", "target"}

// keep reports whether dir must be kept although it's a target, because it
// may hold real files. Inside a git repo, a folder git tracks files in is
// kept, and so is one git can't answer for, and so is a gitOnly folder git
// doesn't ignore: it may hold new files not committed yet. Outside a repo,
// only gitOnly names are kept, and git isn't run.
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
	if err != nil || len(out) > 0 {
		return true
	}
	return slices.Contains(gitOnly, filepath.Base(dir)) && !ignored(ctx, dir)
}

// ignored reports whether an ignore rule that names dir (build/, /dist,
// **/target, apps/web/out) covers it. A rule that doesn't name it, like a
// catch-all * in a repo around the whole home folder, says nothing about
// what's inside, so it doesn't count; nor does a ! rule.
func ignored(ctx context.Context, dir string) bool {
	name := filepath.Base(dir)
	cmd := exec.CommandContext(ctx, "git", "-C", filepath.Dir(dir), "check-ignore", "-v", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(name + "/\x00") // -z needs --stdin
	out, err := cmd.Output()
	if err != nil {
		return false // 1: not ignored; anything else: can't tell
	}
	f := strings.Split(string(out), "\x00") // source, line, pattern, path
	if len(f) < 4 || strings.HasPrefix(f[2], "!") { // git already exits 1 for a ! match; belt and braces
		return false
	}
	return path.Base(strings.TrimSuffix(f[2], "/")) == name
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
