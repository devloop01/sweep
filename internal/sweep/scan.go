package sweep

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
)

// Candidate is a vetted target folder.
type Candidate struct {
	Path string // absolute
	Size int64  // allocated bytes, like du; each hard-linked file counted once per run
}

// Scan finds the target folders under root, which must already be resolved
// and passed by CheckRoot. It never follows symlinks, never enters .git or
// *.app folders, and does not look inside a match. Results are sorted by
// size (biggest first), then path.
func Scan(root string, t Targets, workers int) []Candidate {
	workers = max(workers, 1)
	found := make(chan string)
	go func() {
		find(root, t, workers, found)
		close(found)
	}()

	var (
		mu      sync.Mutex
		results []measured
		wg      sync.WaitGroup
	)
	for range workers {
		wg.Go(func() {
			for p := range found {
				if !Vet(root, p, t) {
					continue
				}
				m := measure(p)
				mu.Lock()
				results = append(results, m)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	// A file hard-linked into several targets is counted against the first
	// one in path order, so sizes don't depend on which worker got there first.
	slices.SortFunc(results, func(a, b measured) int { return strings.Compare(a.path, b.path) })
	seen := map[inode]bool{}
	cands := make([]Candidate, 0, len(results))
	for _, m := range results {
		size := m.own
		for ino, b := range m.shared {
			if !seen[ino] {
				seen[ino] = true
				size += b
			}
		}
		cands = append(cands, Candidate{Path: m.path, Size: size})
	}
	slices.SortFunc(cands, func(a, b Candidate) int {
		return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Path, b.Path))
	})
	return cands
}

// find walks root and sends every folder that matches a target. Each folder
// is read in its own goroutine; sem bounds how many read at once. sem is
// only held around ReadDir, so the walk can't deadlock on itself.
func find(root string, t Targets, workers int, found chan<- string) {
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var walk func(dir string)
	walk = func(dir string) {
		sem <- struct{}{}
		entries, err := os.ReadDir(dir)
		<-sem
		if err != nil {
			return // ponytail: unreadable folders are skipped silently, like fd
		}
		for _, e := range entries {
			// DirEntry types come from lstat: a symlink to a folder is not a dir here.
			if !e.IsDir() || e.Name() == ".git" || isApp(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if t.Match(p) {
				found <- p
				continue // prune: never look inside a match
			}
			wg.Go(func() { walk(p) })
		}
	}
	wg.Go(func() { walk(root) })
	wg.Wait()
}

// Vet reports whether p is safe to delete right now: strictly inside root,
// reached without passing through a symlink or a .git / *.app folder, a real
// directory (not a symlink), and still a target.
func Vet(root, p string, t Targets) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	for _, c := range components(rel) {
		if c == ".git" || isApp(c) {
			return false
		}
	}
	// Lstat only checks p itself; a parent swapped for a symlink would
	// otherwise lead outside root.
	parent := filepath.Dir(p)
	if real, err := filepath.EvalSymlinks(parent); err != nil || real != parent {
		return false
	}
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir() && t.Match(p)
}

type inode struct{ dev, ino uint64 }

type measured struct {
	path   string
	own    int64           // blocks of everything not hard-linked
	shared map[inode]int64 // blocks of hard-linked files, by inode
}

// measure adds up allocated blocks under p like `du -sk`, without
// following symlinks.
func measure(p string) measured {
	m := measured{path: p, shared: map[inode]int64{}}
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // count what can be read
		}
		info, err := d.Info() // lstat
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		b := st.Blocks * 512
		if !d.IsDir() && st.Nlink > 1 {
			m.shared[inode{uint64(st.Dev), st.Ino}] = b
		} else {
			m.own += b
		}
		return nil
	})
	return m
}
