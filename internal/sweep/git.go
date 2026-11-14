//go:build darwin && arm64

package sweep

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	_, repo := repoTop(dirs[0])
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

// repoTop is the nearest folder above dir with a .git entry: the top of the
// repository git answers for when asked about dir.
func repoTop(dir string) (top string, ok bool) {
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		if d == filepath.Dir(d) {
			return "", false
		}
	}
}

// repos is keep for a whole scan: it asks each repository once which
// folders hold tracked files, and keeps one git running per repository to
// ask about ignore rules, where keep runs git for every folder. In a
// monorepo with thousands of packages that's two git runs, not thousands.
//
// Its answers only decide what the scan lists. Remove asks keep again about
// each folder, with git's own answer for that folder, before deleting it.
type repos struct {
	t    Targets
	mu   sync.Mutex
	tops map[string]*repo
}

type repo struct {
	top  string
	once sync.Once
	// tracked is the target folders with tracked files, as lower-case paths
	// from top; nil when git couldn't list them, and every folder is kept.
	tracked map[string]bool

	mu      sync.Mutex // one question to check at a time
	check   *exec.Cmd  // git check-ignore, answering path by path
	in      io.WriteCloser
	out     *bufio.Reader
	noCheck bool // check failed: ask the slow way from now on
}

func (rs *repos) repo(top string) *repo {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.tops == nil {
		rs.tops = map[string]*repo{}
	}
	r := rs.tops[top]
	if r == nil {
		r = &repo{top: top}
		rs.tops[top] = r
	}
	return r
}

// keepAll is keepAll for folders in the same parent folder.
func (rs *repos) keepAll(dirs []string) []bool {
	keeps := make([]bool, len(dirs))
	if len(dirs) == 0 {
		return keeps
	}
	top, inRepo := repoTop(dirs[0])
	var slow []int // dirs to ask git about one folder at a time
	for i, dir := range dirs {
		name := filepath.Base(dir)
		rel, err := filepath.Rel(top, dir)
		switch _, gitErr := os.Lstat(filepath.Join(dir, ".git")); {
		case gitErr == nil:
			keeps[i] = true
		case !inRepo:
			keeps[i] = slices.Contains(gitOnly, name)
		case err != nil || !plain(rel):
			// ponytail: git may spell an é differently from the disk
			// (core.precomposeunicode), so such paths aren't looked up in
			// its list.
			slow = append(slow, i)
		default:
			r := rs.repo(top)
			r.once.Do(func() { r.tracked = trackedTargets(top, rs.t) })
			switch {
			case r.tracked == nil || r.tracked[strings.ToLower(filepath.ToSlash(rel))]:
				keeps[i] = true
			case slices.Contains(gitOnly, name):
				keeps[i] = !r.ignored(filepath.ToSlash(rel))
			}
		}
	}
	if len(slow) > 0 {
		var ds []string
		for _, i := range slow {
			ds = append(ds, dirs[i])
		}
		for j, k := range keepAll(ds) {
			keeps[slow[j]] = k
		}
	}
	return keeps
}

// plain reports whether s is printable ASCII: bytes git and the disk can
// only spell one way.
func plain(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r > '~' })
}

// trackedTargets lists the target folders under top that hold tracked files
// (or are tracked themselves, like a submodule), as lower-case paths from
// top: APFS ignores case, so Build/ in git's list is build/ on disk. It
// returns nil when git can't say.
func trackedTargets(top string, t Targets) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := git(ctx, top, "ls-files", "-z")
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return nil
	}
	tracked := map[string]bool{}
	sc := bufio.NewScanner(out)
	sc.Buffer(nil, 1<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, 0); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, errors.New("git ls-files: cut short")
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		// Every folder on the way to the file, and the entry itself.
		p := "/" + sc.Text()
		for i := 1; i <= len(p); i++ {
			if i < len(p) && p[i] != '/' {
				continue
			}
			if low := strings.ToLower(p[:i]); t.Match(p[:i]) || t.Match(low) {
				tracked[low[1:]] = true
			}
		}
	}
	if werr := cmd.Wait(); sc.Err() != nil || werr != nil {
		return nil
	}
	return tracked
}

// ignored is ignored for one folder, rel from the repo's top. It asks a git
// that stays running for the whole scan; if that fails, it asks a new git
// each time, like keep.
func (r *repo) ignored(rel string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := path.Base(rel)
	if !r.noCheck {
		if pattern, ok := r.ask(rel); ok {
			return pattern != "" && !strings.HasPrefix(pattern, "!") && path.Base(strings.TrimSuffix(pattern, "/")) == name
		}
		r.noCheck = true
		r.stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ignored(ctx, filepath.Join(r.top, filepath.FromSlash(path.Dir(rel))), []string{name})[name]
}

// ask puts rel to the running check-ignore and returns the pattern that
// covers it, "" for none. With -n git answers for every path, so each
// question has exactly one answer: source, line, pattern, path.
func (r *repo) ask(rel string) (pattern string, ok bool) {
	if r.check == nil {
		// No timeout on the command: it lives as long as the scan.
		cmd := git(context.Background(), r.top, "check-ignore", "-v", "-n", "-z", "--stdin")
		in, err1 := cmd.StdinPipe()
		out, err2 := cmd.StdoutPipe()
		if err1 != nil || err2 != nil || cmd.Start() != nil {
			return "", false
		}
		r.check, r.in, r.out = cmd, in, bufio.NewReader(out)
	}
	// A git that stops answering is killed, which ends the read below.
	cmd := r.check
	timer := time.AfterFunc(10*time.Second, func() { cmd.Process.Kill() })
	defer timer.Stop()
	if _, err := io.WriteString(r.in, rel+"/\x00"); err != nil {
		return "", false
	}
	var f [4]string
	for i := range f {
		s, err := r.out.ReadString(0)
		if err != nil {
			return "", false
		}
		f[i] = strings.TrimSuffix(s, "\x00")
	}
	if f[3] != rel+"/" {
		return "", false // not the answer to this question
	}
	return f[2], true
}

// stop ends the running check-ignore, if any.
func (r *repo) stop() {
	if r.check == nil {
		return
	}
	r.in.Close()
	r.check.Process.Kill()
	r.check.Wait()
	r.check = nil
}

// close ends every git left running.
func (rs *repos) close() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, r := range rs.tops {
		r.mu.Lock()
		r.stop()
		r.mu.Unlock()
	}
}
