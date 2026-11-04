package sweep

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Project is a folder that holds a project: Dir, and Kind, the tool or
// framework it uses, like "Turborepo" or "Next.js".
type Project struct {
	Dir, Kind string
}

// FindProject returns the project dir belongs to, looking no higher than
// root: the outermost monorepo above it, or else the nearest folder with a
// package.json. Folders outside any project return false.
func FindProject(root, dir string) (p Project, ok bool) {
	for d := dir; ; d = filepath.Dir(d) {
		rel, err := filepath.Rel(root, d)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return p, ok
		}
		// Only a few names are checked on the way up; the folder is read
		// once, for its framework, when it's the project.
		if kind := monorepo(d); kind != "" {
			p, ok = Project{d, kind}, true // keep looking: the outermost one wins
		} else if !ok && hasFile(d, "package.json") {
			p, ok = Project{d, framework(d)}, true
		}
		if d == root || d == filepath.Dir(d) {
			return p, ok
		}
	}
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
	}
	return ""
}

// hasWorkspaces reports whether a package.json lists workspaces (npm, Yarn,
// Bun): ["apps/*"] or {"packages": ["apps/*"]}. An empty list doesn't count.
func hasWorkspaces(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var pkg struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&pkg) != nil || pkg.Workspaces == nil {
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

func framework(dir string) string {
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
