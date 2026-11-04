package sweep

import (
	"os"
	"path/filepath"
	"testing"
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
