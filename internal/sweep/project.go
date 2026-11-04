package sweep

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Project is a folder that holds a project: Dir, and Kind, the tool or
// framework it uses, like "Turborepo" or "Next.js".
type Project struct {
	Dir, Kind string
}

// FindProject returns the project dir belongs to, looking no higher than
// root: the outermost monorepo above it, or else the nearest folder with a
// package.json, Cargo.toml or pyproject.toml. Folders outside any project
// return false.
func FindProject(root, dir string) (Project, bool) { return new(Finder).Find(root, dir) }

// Finder is FindProject that remembers what each folder is, so a scan that
// finds many folders in one monorepo reads its files once. The zero value is
// ready to use, from many goroutines at once.
type Finder struct {
	mu   sync.Mutex
	dirs map[string]kinds
}

// kinds is what a folder is: a monorepo, a project, or neither ("").
type kinds struct{ mono, project string }

// Find is FindProject.
func (f *Finder) Find(root, dir string) (p Project, ok bool) {
	for d := dir; ; d = filepath.Dir(d) {
		rel, err := filepath.Rel(root, d)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return p, ok
		}
		if k := f.kinds(d); k.mono != "" {
			p, ok = Project{d, k.mono}, true // keep looking: the outermost one wins
		} else if !ok && k.project != "" {
			p, ok = Project{d, k.project}, true
		}
		if d == root || d == filepath.Dir(d) {
			return p, ok
		}
	}
}

func (f *Finder) kinds(d string) kinds {
	f.mu.Lock()
	k, seen := f.dirs[d]
	f.mu.Unlock()
	if seen {
		return k
	}
	// Only a few names are checked; the folder is read, for its framework,
	// only when it's a project.
	if k.mono = monorepo(d); k.mono == "" {
		switch {
		case hasFile(d, "package.json"):
			k.project = framework(d)
		case hasFile(d, "Cargo.toml") && isTauri(filepath.Dir(d)) && filepath.Base(d) == "src-tauri":
			// A Tauri app's Rust half: the app is the web project above.
		case hasFile(d, "Cargo.toml"):
			k.project = "Rust"
		case hasFile(d, "pyproject.toml"):
			k.project = "Python"
		}
	}
	f.mu.Lock()
	if f.dirs == nil {
		f.dirs = map[string]kinds{}
	}
	f.dirs[d] = k
	f.mu.Unlock()
	return k
}

func hasFile(dir, name string) bool {
	_, err := os.Lstat(filepath.Join(dir, name))
	return err == nil
}

func monorepo(dir string) string {
	switch {
	case hasFile(dir, "turbo.json"):
		return "Turborepo"
	case hasFile(dir, "nx.json"):
		return "Nx"
	case hasFile(dir, "lerna.json"):
		return "Lerna"
	case hasFile(dir, "pnpm-workspace.yaml"):
		return "pnpm workspace"
	case hasWorkspaces(filepath.Join(dir, "package.json")):
		return "workspaces"
	case cargoWorkspace(filepath.Join(dir, "Cargo.toml")):
		return "Cargo workspace"
	}
	return ""
}

// readSmall reads up to 1 MB of path, if it's a regular file. It doesn't
// follow a symlink or wait on a pipe or device: a project file in the tree
// being scanned can't hang the scan.
func readSmall(path string) ([]byte, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	return b, err == nil
}

// cargoWorkspace reports whether a Cargo.toml has a [workspace] table.
func cargoWorkspace(path string) bool {
	b, _ := readSmall(path)
	for line := range strings.Lines(string(b)) {
		if strings.TrimSpace(line) == "[workspace]" {
			return true
		}
	}
	return false
}

// hasWorkspaces reports whether a package.json lists workspaces (npm, Yarn,
// Bun): ["apps/*"] or {"packages": ["apps/*"]}. An empty list doesn't count.
func hasWorkspaces(path string) bool {
	b, ok := readSmall(path)
	if !ok {
		return false
	}
	var pkg struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.Unmarshal(b, &pkg) != nil || pkg.Workspaces == nil {
		return false
	}
	var list []string
	if json.Unmarshal(pkg.Workspaces, &list) != nil {
		var obj struct{ Packages []string }
		json.Unmarshal(pkg.Workspaces, &obj)
		list = obj.Packages
	}
	return len(list) > 0
}

// frameworks are found by their config file, first match wins; a name
// ending in "." matches any extension (next.config.js, next.config.ts).
var frameworks = []struct{ file, kind string }{
	{"next.config.", "Next.js"},
	{"svelte.config.", "SvelteKit"},
	{"nuxt.config.", "Nuxt"},
	{"astro.config.", "Astro"},
	{"remix.config.", "Remix"},
	{"angular.json", "Angular"},
	{"docusaurus.config.", "Docusaurus"},
	{"gatsby-config.", "Gatsby"},
	{"vite.config.", "Vite"},
}

// isTauri reports whether dir is a Tauri app: a web project with its Rust
// half in src-tauri.
func isTauri(dir string) bool {
	return hasFile(dir, "package.json") && hasFile(filepath.Join(dir, "src-tauri"), "Cargo.toml")
}

func framework(dir string) string {
	if isTauri(dir) {
		return "Tauri"
	}
	entries, _ := os.ReadDir(dir)
	for _, f := range frameworks {
		for _, e := range entries {
			if n := e.Name(); n == f.file || strings.HasSuffix(f.file, ".") && strings.HasPrefix(n, f.file) {
				return f.kind
			}
		}
	}
	return "Node"
}
