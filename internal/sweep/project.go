package sweep

import (
	"bytes"
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
		names := map[string]bool{}
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			names[e.Name()] = true
		}
		if kind := monorepo(d, names); kind != "" {
			p, ok = Project{d, kind}, true // keep looking: the outermost one wins
		} else if !ok && names["package.json"] {
			p, ok = Project{d, framework(names)}, true
		}
		if d == root || d == filepath.Dir(d) {
			return p, ok
		}
	}
}

func monorepo(dir string, names map[string]bool) string {
	switch {
	case names["turbo.json"]:
		return "Turborepo"
	case names["nx.json"]:
		return "Nx"
	case names["lerna.json"]:
		return "Lerna"
	case names["pnpm-workspace.yaml"]:
		return "pnpm workspace"
	case names["package.json"] && hasWorkspaces(filepath.Join(dir, "package.json")):
		return "workspaces"
	}
	return ""
}

// hasWorkspaces reports whether a package.json declares workspaces (npm,
// Yarn, Bun).
func hasWorkspaces(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var pkg struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&pkg) != nil {
		return false
	}
	w := bytes.TrimSpace(pkg.Workspaces)
	return len(w) > 0 && !bytes.Equal(w, []byte("null"))
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

func framework(names map[string]bool) string {
	for _, f := range frameworks {
		for n := range names {
			if n == f.file || strings.HasSuffix(f.file, ".") && strings.HasPrefix(n, f.file) {
				return f.kind
			}
		}
	}
	return "Node"
}
