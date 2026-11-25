//go:build darwin && arm64

package sweep

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// Caches are found from their default folders, only for tools on PATH
// (never Apple's /usr/bin stubs), and only when something can be freed.
func TestFindCaches(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	for _, tool := range []string{"npm", "pnpm", "go", "uv"} { // no brew, bun or pip3
		os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\n"), 0o755)
	}
	t.Setenv("PATH", bin)
	write := func(p string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, 10000), 0o644)
	}
	write(filepath.Join(home, ".npm/_cacache/blob"))
	write(filepath.Join(home, "Library/pnpm/store/v10/blob"))  // pnpm isn't listed
	write(filepath.Join(home, "Library/Caches/Homebrew/blob")) // brew isn't installed
	write(filepath.Join(home, "elsewhere/gocache/blob"))       // GOCACHE points here
	os.MkdirAll(filepath.Join(home, ".cache/uv"), 0o755)       // empty: nothing to free
	env := map[string]string{"GOCACHE": filepath.Join(home, "elsewhere/gocache")}

	var mu sync.Mutex
	var got []string
	FindCaches(home, func(k string) string { return env[k] }, nil, func(c Cache) {
		mu.Lock()
		defer mu.Unlock()
		if c.Size <= 0 {
			t.Errorf("%s: size %d", c.Name, c.Size)
		}
		rel, _ := filepath.Rel(home, c.Dir)
		got = append(got, c.Name+" "+rel)
	})
	slices.Sort(got)
	want := []string{"Go build cache elsewhere/gocache", "npm .npm/_cacache"}
	if !slices.Equal(got, want) {
		t.Errorf("found %q, want %q", got, want)
	}
}

// Clearing runs the tool's command from home and reports what it freed:
// the size before minus after, even when the command fails partway.
func TestClearCache(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "cache")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a"), make([]byte, 100000), 0o644)
	os.WriteFile(filepath.Join(dir, "b"), make([]byte, 100000), 0o644)
	c := Cache{Name: "x", Dir: dir, Clean: []string{"x", "clean"}, Size: measure(dir, nil).own}

	defer func(old func(string, []string) ([]byte, error)) { runClean = old }(runClean)
	var ranIn string
	var ran []string
	runClean = func(wd string, args []string) ([]byte, error) {
		ranIn, ran = wd, args
		os.Remove(filepath.Join(dir, "a")) // clears half, then fails
		return []byte("working\nx: disk on fire\n"), errors.New("exit status 1")
	}
	freed, err := ClearCache(home, c)
	if ranIn != home || !slices.Equal(ran, c.Clean) {
		t.Errorf("ran %v in %s", ran, ranIn)
	}
	if want := c.Size - measure(dir, nil).own; freed != want || freed <= 0 {
		t.Errorf("freed %d, want %d", freed, want)
	}
	if err == nil || err.Error() != "x clean: x: disk on fire" {
		t.Errorf("error %v", err)
	}
}
