//go:build darwin && arm64

package sweep

import (
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckRoot(t *testing.T) {
	const home = "/Users/me"
	homes := []string{home}
	refused := []string{
		"/", "/Users", home, "/USERS/ME", home + "/",
		home + "/Library", home + "/Library/x", home + "/library/Caches",
		home + "/Applications", home + "/Applications/x",
		"/usr", "/usr/local", "/USR/local", "/System", "/System/Volumes/Data",
		"/Applications", "/Library/Caches", "/opt/homebrew", "/private", "/private/tmp",
		"/Volumes", "/volumes",
		"/x/Foo.app", "/x/Foo.APP/Contents", home + "/Code/My.app/Contents/Resources",
		home + "/.nvm", home + "/.vscode/extensions", home + "/.local/share/pnpm", home + "/.Bun",
	}
	for _, dir := range refused {
		if err := CheckRoot(dir, homes, SystemRoots); err == nil {
			t.Errorf("CheckRoot(%q) allowed, want refused", dir)
		}
	}
	allowed := []string{
		"/usrdata", "/privatefoo", "/Systems", "/optional",
		home + "/Libraryfoo", home + "/Applicationsfoo", home + "/Documents/Code",
		home + "/Code/Library", "/Users/other", "/Volumes/External",
		"/x/app", "/x/Fooapp", "/x/Foo.apps",
		home + "/Code/.hidden", home + "/Code/proj/.vscode",
	}
	for _, dir := range allowed {
		if err := CheckRoot(dir, homes, SystemRoots); err != nil {
			t.Errorf("CheckRoot(%q) refused: %v", dir, err)
		}
	}
	for _, bad := range [][]string{nil, {""}, {home, "relative/home"}} {
		if err := CheckRoot("/Users/me/Code", bad, SystemRoots); err == nil {
			t.Errorf("CheckRoot with homes %q allowed, want refused", bad)
		}
	}
	// $HOME overridden (or sudo): the home macOS has on record still counts.
	both := []string{"/var/root", home}
	for _, dir := range []string{home, home + "/Library/x", home + "/.nvm"} {
		if err := CheckRoot(dir, both, SystemRoots); err == nil {
			t.Errorf("CheckRoot(%q) with homes %q allowed, want refused", dir, both)
		}
	}
}

// Real folders: a symlink to home resolves to home and is refused; a
// sibling whose name only starts like a refused root is allowed.
func TestResolveAndCheck(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	usr := filepath.Join(tmp, "usr")
	usrdata := filepath.Join(tmp, "usrdata")
	for _, d := range []string{home, filepath.Join(usr, "local"), usrdata} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	realHome, err := Resolve(home)
	if err != nil {
		t.Fatal(err)
	}
	realUsr, _ := Resolve(usr)
	system := []string{realUsr}

	check := func(dir string) error {
		r, err := Resolve(dir)
		if err != nil {
			t.Fatal(err)
		}
		return CheckRoot(r, []string{realHome}, system)
	}
	if check(link) == nil {
		t.Error("symlink to home allowed")
	}
	if check(filepath.Join(usr, "local")) == nil {
		t.Error("usr/local allowed")
	}
	if err := check(usrdata); err != nil {
		t.Errorf("usrdata refused: %v", err)
	}

	if _, err := Resolve(filepath.Join(tmp, "missing")); err == nil {
		t.Error("Resolve of missing dir succeeded")
	}
	file := filepath.Join(tmp, "file")
	os.WriteFile(file, nil, 0o644)
	if _, err := Resolve(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("Resolve of file: %v", err)
	}
}

// An overridden $HOME doesn't hide the home macOS has on record.
func TestHomesIgnoresOverriddenHOME(t *testing.T) {
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	u, err := user.Current()
	if err != nil {
		t.Skip("no user record:", err)
	}
	homes := Homes()
	if !slices.Contains(homes, u.HomeDir) || !slices.Contains(homes, filepath.Clean(fake)) {
		t.Errorf("Homes() = %q, want both %q and %q", homes, u.HomeDir, fake)
	}
}
