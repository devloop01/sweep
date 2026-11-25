//go:build darwin && arm64

package sweep

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Cache is a package manager's global download cache: one shared folder in
// the home folder, outside any project. sweep never deletes inside it; it
// runs the tool's own clean command, which knows what's safe to drop.
type Cache struct {
	Name  string   // "npm", "Go modules": the tool, not the word cache
	Dir   string   // the folder sized, absolute
	Clean []string // the tool's command that clears it, run from the home folder
	Size  int64    // bytes clearing can free: files with no other hard link
}

// cacheSpec is where a tool keeps its cache by default and how to clear it.
// The folder comes from the defaults and the tool's environment overrides,
// not from asking the tool: package managers are slow to start (seconds,
// behind version-manager shims) and some download or prompt on first run.
type cacheSpec struct {
	name  string
	tool  string
	dir   func(home string, env func(string) string) string
	clean []string
}

// orHome is env's value for key if set, else home joined with rel.
func orHome(key string, rel ...string) func(string, func(string) string) string {
	return func(home string, env func(string) string) string {
		return cmp.Or(env(key), filepath.Join(append([]string{home}, rel...)...))
	}
}

// ponytail: env overrides only; a cache moved with `go env -w` or an
// .npmrc isn't found. Clearing still measures the folder shown, so the
// freed size stays true.
//
// pnpm's store is left out: on APFS pnpm clones files into projects, so a
// store file a project uses looks unshared, and its size would claim many
// GB that `pnpm store prune` doesn't free. Add it when that can be told.
var cacheSpecs = []cacheSpec{
	{"npm", "npm", func(home string, env func(string) string) string {
		return filepath.Join(cmp.Or(env("npm_config_cache"), filepath.Join(home, ".npm")), "_cacache")
	}, []string{"npm", "cache", "clean", "--force"}},
	{"bun", "bun", orHome("BUN_INSTALL_CACHE_DIR", ".bun/install/cache"), []string{"bun", "pm", "cache", "rm"}},
	{"Go build", "go", orHome("GOCACHE", "Library/Caches/go-build"), []string{"go", "clean", "-cache"}},
	{"Go modules", "go", func(home string, env func(string) string) string {
		if d := env("GOMODCACHE"); d != "" {
			return d
		}
		return filepath.Join(cmp.Or(strings.Split(env("GOPATH"), ":")[0], filepath.Join(home, "go")), "pkg/mod")
	}, []string{"go", "clean", "-modcache"}},
	// brew cleanup also removes old versions of installed formulae, as
	// Homebrew does by itself every 30 days; the dialog shows the command.
	{"Homebrew", "brew", orHome("HOMEBREW_CACHE", "Library/Caches/Homebrew"), []string{"brew", "cleanup", "--prune=all"}},
	{"pip", "pip3", orHome("PIP_CACHE_DIR", "Library/Caches/pip"), []string{"pip3", "cache", "purge"}},
	{"uv", "uv", func(home string, env func(string) string) string {
		return cmp.Or(env("UV_CACHE_DIR"), filepath.Join(cmp.Or(env("XDG_CACHE_HOME"), filepath.Join(home, ".cache")), "uv"))
	}, []string{"uv", "cache", "clean"}},
}

// FindCaches calls found, from several goroutines, for each cache whose tool
// is installed and whose folder holds something clearing would free. It
// returns once all are measured, or progress is stopped. A tool found only
// in /usr/bin is skipped: there, pip3 is Apple's stub that asks to install
// the developer tools.
func FindCaches(home string, env func(string) string, progress *Progress, found func(Cache)) {
	var wg sync.WaitGroup
	for _, s := range cacheSpecs {
		path, err := exec.LookPath(s.tool)
		if err != nil || strings.HasPrefix(path, "/usr/bin/") {
			continue
		}
		dir := s.dir(home, env)
		if fi, err := os.Lstat(dir); dir == "" || err != nil || !fi.IsDir() {
			continue
		}
		wg.Go(func() {
			if size := measure(dir, progress).own; size > 0 && !progress.stopped() {
				found(Cache{s.name, dir, s.clean, size})
			}
		})
	}
	wg.Wait()
}

// runClean runs a clean command; tests swap it so nothing real is cleared.
var runClean = func(home string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = home
	cmd.Stdin = nil // no prompts: anything that asks gets end of input
	cmd.Env = append(os.Environ(), "COREPACK_ENABLE_DOWNLOAD_PROMPT=0", "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1")
	return cmd.CombinedOutput()
}

// ClearCache runs c's clean command and returns how much it freed: the
// folder's size before minus after, so a command that keeps some of it
// (brew keeps the latest downloads it still needs) isn't overcounted.
func ClearCache(home string, c Cache) (freed int64, err error) {
	out, err := runClean(home, c.Clean)
	after := measure(c.Dir, nil).own
	if err != nil {
		msg := strings.TrimSpace(string(bytes.TrimSpace(out)))
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return max(c.Size-after, 0), fmt.Errorf("%s: %s", strings.Join(c.Clean, " "), cmp.Or(msg, err.Error()))
	}
	return max(c.Size-after, 0), nil
}
