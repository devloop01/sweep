package sweep

import (
	"errors"
	"fmt"
	"os"
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

// CheckRoot refuses dir (already resolved) when sweeping it could reach
// system folders, the user's Library, or app bundles. home must be the
// resolved home folder; system is normally SystemRoots.
//
// Paths are compared per component and ignoring case: APFS is
// case-insensitive and EvalSymlinks keeps the case the user typed, so
// /USR/local must count as /usr/local.
func CheckRoot(dir, home string, system []string) error {
	if !filepath.IsAbs(home) {
		return errors.New("cannot determine your home folder, refusing to run")
	}
	refused := within(home, dir) || // dir is home, or contains it (this covers "/")
		within(dir, filepath.Join(home, "Library")) ||
		within(dir, filepath.Join(home, "Applications")) ||
		equal(dir, "/Volumes") ||
		inApp(dir)
	for _, s := range system {
		refused = refused || within(dir, s)
	}
	if refused {
		return fmt.Errorf("refusing to run in %s — run it inside a projects folder instead", dir)
	}
	return nil
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
