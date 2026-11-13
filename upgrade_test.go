//go:build darwin && arm64

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.7.0", "v0.6.0", true},
		{"v0.10.0", "v0.9.3", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.6.0", "v0.6.0", false},
		{"v0.6.0", "v0.6.0-2-g386b6ed", false},               // make build after the tag
		{"v0.6.0", "v0.6.1-0.20261103162539-386b6ed", false}, // go install pseudo-version
		{"v0.5.0", "v0.6.0", false},
		{"v0.7.0", "1be5122", false}, // make build in a clone without tags: not a release to compare
		{"v0.7.0", "dev", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGitHub serves a v9.9.9 release whose binary is a script printing its
// version, with SHA256SUMS listing sum.
func fakeGitHub(t *testing.T, bin string, sum string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/v9.9.9", http.StatusFound)
		case "/releases/download/v9.9.9/" + asset:
			io.WriteString(w, bin)
		case "/releases/download/v9.9.9/SHA256SUMS":
			io.WriteString(w, sum+"  "+asset+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestUpgrade(t *testing.T) {
	bin := "#!/bin/sh\necho v9.9.9\n"
	good := sha256.Sum256([]byte(bin))
	for _, c := range []struct {
		name, sum, current string
		replaced           bool
	}{
		{"replaces the binary", hex.EncodeToString(good[:]), "v0.6.0", true},
		{"bad checksum changes nothing", hex.EncodeToString(make([]byte, 32)), "v0.6.0", false},
		{"up to date changes nothing", hex.EncodeToString(good[:]), "v9.9.9", false},
		{"a build without a version is left alone", hex.EncodeToString(good[:]), "1be5122", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "sweep")
			os.WriteFile(exe, []byte("old"), 0o755)
			err := upgrade(fakeGitHub(t, bin, c.sum), exe, c.current, io.Discard)
			got, _ := os.ReadFile(exe)
			if c.replaced && (err != nil || string(got) != bin) {
				t.Errorf("err %v, binary %q", err, got)
			}
			if !c.replaced && string(got) != "old" {
				t.Errorf("binary changed to %q", got)
			}
			if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
				t.Errorf("left files behind: %v", entries)
			}
		})
	}
}
