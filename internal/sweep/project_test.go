package sweep

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFindProject(t *testing.T) {
	top := t.TempDir()
	root := filepath.Join(top, "code")
	for path, body := range map[string]string{
		"package.json":                       `{"workspaces": ["*"]}`, // above root: ignored
		"code/mono/turbo.json":               `{}`,
		"code/mono/package.json":             `{}`,
		"code/mono/apps/web/package.json":    `{}`,
		"code/mono/apps/web/next.config.mjs": ``,
		"code/site/package.json":             `{"scripts": {"x": "workspaces"}}`,
		"code/site/svelte.config.js":         ``,
		"code/yarn/package.json":             `{"workspaces": {"packages": ["a"]}}`,
		"code/yarn/a/package.json":           `{}`,
		"code/plain/package.json":            `{}`,
		"code/empty/package.json":            `{"workspaces": []}`, // not a monorepo
		"code/empty/app/package.json":        `{}`,
		"code/loose/x":                       ``,
		"code/rs/Cargo.toml":                 "[workspace]\nmembers = [\"cli\"]\n",
		"code/rs/cli/Cargo.toml":             "[package]\nname = \"cli\"\n",
		"code/tool/Cargo.toml":               "[package]\n# [workspace] in a comment doesn't count\n",
		"code/py/pyproject.toml":             ``,
		"code/desk/package.json":             `{}`,
		"code/desk/vite.config.ts":           ``,
		"code/desk/src-tauri/Cargo.toml":     "[package]\nname = \"desk\"\n",
		"code/rsonly/src-tauri/Cargo.toml":   "[package]\n", // no web project above: plain Rust
	} {
		p := filepath.Join(top, path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		dir, want, kind string
	}{
		{"mono", "mono", "Turborepo"},
		{"mono/apps/web", "mono", "Turborepo"}, // a project inside a monorepo is part of it
		{"mono/apps/web/.vercel", "mono", "Turborepo"},
		{"site", "site", "SvelteKit"},
		{"yarn/a", "yarn", "workspaces"},
		{"plain", "plain", "Node"},
		{"empty/app", "empty/app", "Node"},
		{"rs/cli", "rs", "Cargo workspace"},
		{"tool", "tool", "Rust"},
		{"py", "py", "Python"},
		{"desk", "desk", "Tauri"},
		{"desk/src-tauri", "desk", "Tauri"}, // target lives here: same project as node_modules
		{"rsonly/src-tauri", "rsonly/src-tauri", "Rust"},
		{"loose", "", ""},
		{".", "", ""},
	} {
		p, ok := FindProject(root, filepath.Join(root, c.dir))
		got := ""
		if ok {
			got, _ = filepath.Rel(root, p.Dir)
		}
		if got != c.want || p.Kind != c.kind {
			t.Errorf("%s: got %q %q, want %q %q", c.dir, got, p.Kind, c.want, c.kind)
		}
	}
}

// A project file that's a pipe, or a symlink to one (a tarball can carry
// either), is skipped, not opened: opening a pipe waits for a writer, which
// would hang the scan.
func TestFindProjectSkipsPipes(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"fifo", "linked"} {
		os.MkdirAll(filepath.Join(root, dir, "node_modules"), 0o755)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo/package.json"), 0o644); err != nil {
		t.Skip("mkfifo:", err)
	}
	os.Symlink("../fifo/package.json", filepath.Join(root, "linked/package.json"))
	os.Symlink("../fifo/package.json", filepath.Join(root, "linked/Cargo.toml"))

	done := make(chan bool)
	go func() {
		FindProject(root, filepath.Join(root, "fifo"))
		FindProject(root, filepath.Join(root, "linked"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("FindProject hung on a pipe")
	}
}

// One Finder answers like FindProject, from many goroutines at once.
func TestFinderMatchesFindProject(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"mono/turbo.json", "mono/apps/web/package.json", "solo/package.json"} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte("{}"), 0o644)
	}
	var f Finder
	var wg sync.WaitGroup
	for range 8 {
		for _, d := range []string{"mono/apps/web", "mono", "solo", "."} {
			wg.Go(func() {
				got, gok := f.Find(root, filepath.Join(root, d))
				want, wok := FindProject(root, filepath.Join(root, d))
				if got != want || gok != wok {
					t.Errorf("%s: Finder %v %v, FindProject %v %v", d, got, gok, want, wok)
				}
			})
		}
	}
	wg.Wait()
}
