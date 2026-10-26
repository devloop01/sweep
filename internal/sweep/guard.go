//go:build darwin && arm64

package sweep

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// SystemRoots are refused as DIR, along with everything under them.
var SystemRoots = []string{"/System", "/Applications", "/Library", "/opt", "/usr", "/private"}

// Resolve turns dir into an absolute, symlink-free path to an existing directory.
func Resolve(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return real, nil
}

// Homes returns every path that names the user's home folder: $HOME and
// the home macOS has on record, each as given and resolved. $HOME alone
// can't be trusted: any process can set it.
func Homes() []string {
	var raw, homes []string
	if h := os.Getenv("HOME"); h != "" {
		raw = append(raw, h)
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		raw = append(raw, u.HomeDir)
	}
	for _, h := range raw {
		homes = append(homes, filepath.Clean(h))
		if r, err := filepath.EvalSymlinks(h); err == nil {
			homes = append(homes, r)
		}
	}
	return homes
}

// CheckRoot refuses dir (already resolved) when sweeping it could reach
// system folders, the user's Library or tool folders, or app bundles.
// homes is normally Homes(); system is normally SystemRoots.
//
// Paths are compared per component and ignoring case: APFS is
// case-insensitive and EvalSymlinks keeps the case the user typed, so
// /USR/local must count as /usr/local.
func CheckRoot(dir string, homes, system []string) error {
	if len(homes) == 0 {
		return errors.New("cannot determine your home folder, refusing to run")
	}
	refused := equal(dir, "/Volumes") || inApp(dir)
	for _, home := range homes {
		if !filepath.IsAbs(home) {
			return fmt.Errorf("home folder %q is not an absolute path, refusing to run", home)
		}
		refused = refused ||
			within(home, dir) || // dir is home, or contains it (this covers "/")
			within(dir, filepath.Join(home, "Library")) ||
			within(dir, filepath.Join(home, "Applications")) ||
			inDotFolder(dir, home)
	}
	for _, s := range system {
		refused = refused || within(dir, s)
	}
	if refused {
		return fmt.Errorf("refusing to run in %s — run it inside a projects folder instead", dir)
	}
	return nil
}

// inDotFolder reports whether dir is inside a hidden folder directly in
// home, such as ~/.nvm or ~/.vscode: tools keep global installs and editor
// extensions there, with their own node_modules.
func inDotFolder(dir, home string) bool {
	d, h := components(dir), components(home)
	return within(dir, home) && len(d) > len(h) && strings.HasPrefix(d[len(h)], ".")
}

// within reports whether path is root or inside it.
func within(path, root string) bool {
	p, r := components(path), components(root)
	return len(p) >= len(r) && equalFold(p[:len(r)], r)
}

func equal(a, b string) bool {
	return equalFold(components(a), components(b))
}

func equalFold(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func components(p string) []string {
	return strings.FieldsFunc(filepath.Clean(p), func(r rune) bool { return r == filepath.Separator })
}

// inApp reports whether any component of p is a *.app bundle.
func inApp(p string) bool {
	for _, c := range components(p) {
		if isApp(c) {
			return true
		}
	}
	return false
}

func isApp(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".app")
}
