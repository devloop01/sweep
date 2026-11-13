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
// may hold real files. A folder with its own .git is a checkout of its own
// (a nested clone, a worktree), never output, and is kept. Inside a git
// repo, a folder git tracks files in is kept, and so is one git can't
// answer for, and so is a gitOnly folder git doesn't ignore: it may hold new
// files not committed yet. Outside a repo, only gitOnly names are kept, and
// git isn't run.
func keep(dir string) bool {
	return keepAll([]string{dir})[0]
}

// keepAll is keep for several folders in the same parent folder, asking git
// about all of them at once: a project's node_modules, .next and dist cost
// two git runs, not five.
func keepAll(dirs []string) []bool {
	keeps := make([]bool, len(dirs))
	if len(dirs) == 0 {
		return keeps
	}
	parent := filepath.Dir(dirs[0])
	repo := inRepo(dirs[0])
	var ask []int // dirs git has to answer for
	for i, dir := range dirs {
		switch _, err := os.Lstat(filepath.Join(dir, ".git")); {
		case err == nil:
			keeps[i] = true
		case !repo:
			keeps[i] = slices.Contains(gitOnly, filepath.Base(dir))
		default:
			ask = append(ask, i)
		}
	}
	if len(ask) == 0 {
		return keeps
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// :(literal) so a folder name is never read as a git pattern.
	args := []string{"ls-files", "-z", "--"}
	for _, i := range ask {
		args = append(args, ":(literal)"+filepath.Base(dirs[i]))
	}
	out, err := git(ctx, parent, args...).Output()
	// ponytail: without Xcode's command line tools, /usr/bin/git fails (and
	// may offer to install them); every folder in a repo is then kept.
	if err != nil {
		for _, i := range ask {
			keeps[i] = true
		}
		return keeps
	}
	tracked := map[string]bool{} // names with tracked files in them
	for _, f := range strings.Split(string(out), "\x00") {
		name, _, _ := strings.Cut(f, "/") // paths are relative to parent
		tracked[name] = true
	}
	var check []string // gitOnly names with nothing tracked: kept unless ignored
	for _, i := range ask {
		name := filepath.Base(dirs[i])
		if tracked[name] {
			keeps[i] = true
		} else if slices.Contains(gitOnly, name) {
			check = append(check, name)
		}
	}
	if len(check) > 0 {
		ign := ignored(ctx, parent, check)
		for _, i := range ask {
			if name := filepath.Base(dirs[i]); !keeps[i] && slices.Contains(check, name) {
				keeps[i] = !ign[name]
			}
		}
	}
	return keeps
}

// ignored reports which of the folders names in parent are covered by an
// ignore rule that names them (build/, /dist, **/target, apps/web/out). A
// rule that doesn't name the folder, like a catch-all * in a repo around the
// whole home folder, says nothing about what's inside, so it doesn't count;
// nor does a ! rule.
func ignored(ctx context.Context, parent string, names []string) map[string]bool {
	var in strings.Builder
	for _, name := range names {
		in.WriteString(name + "/\x00") // -z needs --stdin
	}
	cmd := git(ctx, parent, "check-ignore", "-v", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		return nil // 1: none ignored; anything else: can't tell
	}
	ign := map[string]bool{}
	f := strings.Split(string(out), "\x00") // per match: source, line, pattern, path
	for ; len(f) >= 4; f = f[4:] {
		name := strings.TrimSuffix(f[3], "/")
		// With -v git reports a ! match too.
		if !strings.HasPrefix(f[2], "!") && path.Base(strings.TrimSuffix(f[2], "/")) == name {
			ign[name] = true
		}
	}
	return ign
}

// repoEnv are the variables that point git at one particular repository or
// index (git rev-parse --local-env-vars). Git sets them for hooks and
// rebase --exec, and some people export GIT_DIR for a dotfiles repo.
var repoEnv = []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT",
	"GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE", "GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
	"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX", "GIT_SHALLOW_FILE", "GIT_COMMON_DIR"}

// git is a git command run in dir, answering for the repository dir is in
// whatever the environment says: with GIT_DIR or GIT_INDEX_FILE inherited,
// git would answer for some other repository, where the folder's files
// aren't tracked.
func git(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(repoEnv, name)
	})
	return cmd
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
