//go:build darwin && arm64

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	repoURL = "https://github.com/devloop01/sweep"
	asset   = "sweep-darwin-arm64"
)

var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// latest asks GitHub for the newest release's tag (pre-releases excluded).
// It reads the redirect from /releases/latest, so there's no API rate limit.
func latest(base string) (string, error) {
	c := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Head(base + "/releases/latest")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	tag := path.Base(resp.Header.Get("Location"))
	if !releaseTag.MatchString(tag) {
		return "", fmt.Errorf("couldn't read the latest version from %s", base)
	}
	return tag, nil
}

// numbers reads X, Y and Z from a version that starts with vX.Y.Z. Anything
// after them (v0.6.0-2-g386b6ed from make, a go install pseudo-version) is
// ignored. ok is false for a version with no such numbers: "dev", or the
// bare commit hash make uses in a clone without tags.
func numbers(v string) (n [3]int, ok bool) {
	got, _ := fmt.Sscanf(v, "v%d.%d.%d", &n[0], &n[1], &n[2])
	return n, got == 3
}

// newer reports whether version a is newer than b, comparing their numbers.
// A version without numbers is neither newer nor older than anything.
func newer(a, b string) bool {
	x, okA := numbers(a)
	y, okB := numbers(b)
	return okA && okB && slices.Compare(x[:], y[:]) > 0
}

// upgrade replaces the binary at exe with the latest release, if it's newer
// than current. The download must match the release's SHA256SUMS and report
// the release's version before it's renamed over exe; until then exe is
// untouched. (Renaming, not overwriting in place: macOS kills a signed
// binary whose file was rewritten.)
func upgrade(base, exe, current string, out io.Writer) error {
	if _, ok := numbers(current); !ok { // "dev", or a commit hash: can't be compared with a release
		return fmt.Errorf("this is a development build; install a release with:\n  curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh")
	}
	tag, err := latest(base)
	if err != nil {
		return err
	}
	if !newer(tag, current) {
		fmt.Fprintf(out, "sweep %s is up to date.\n", current)
		return nil
	}
	fmt.Fprintf(out, "Downloading sweep %s ...\n", tag)
	dl := base + "/releases/download/" + tag + "/"
	bin, err := download(dl + asset)
	if err != nil {
		return err
	}
	sums, err := download(dl + "SHA256SUMS")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(bin)
	if !slices.ContainsFunc(strings.Split(string(sums), "\n"), func(l string) bool {
		f := strings.Fields(l)
		return len(f) == 2 && f[0] == hex.EncodeToString(sum[:]) && f[1] == asset
	}) {
		return fmt.Errorf("checksum mismatch: the download is corrupt. Nothing was changed")
	}

	// The new binary goes next to the old one, so the rename stays on one volume.
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".sweep-upgrade-*")
	if err != nil {
		return fmt.Errorf("can't write to %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	_, err = tmp.Write(bin)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err != nil {
		return err
	}
	if got, err := exec.Command(tmp.Name(), "--version").Output(); err != nil || string(bytes.TrimSpace(got)) != tag {
		return fmt.Errorf("the downloaded binary doesn't report version %s. Nothing was changed", tag)
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return err
	}
	fmt.Fprintf(out, "Upgraded sweep %s → %s\n", current, tag)
	return nil
}

func download(url string) ([]byte, error) {
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("couldn't download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
}
