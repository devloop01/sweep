//go:build darwin && arm64

package sweep

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// Progress lets another goroutine watch a running Scan.
type Progress struct {
	dirs    atomic.Int64
	current atomic.Pointer[string]

	// OnFound, if set, is called (from Scan's goroutines) with each folder as
	// soon as it's measured. Its Size counts files hard-linked into other
	// folders in full; Scan's result has the final sizes.
	OnFound func(Candidate)
}

// Dirs is how many folders the scan has read so far.
func (p *Progress) Dirs() int64 { return p.dirs.Load() }

// Current is the folder the scan read most recently, or "".
func (p *Progress) Current() string {
	if s := p.current.Load(); s != nil {
		return *s
	}
	return ""
}

// Candidate is a vetted target folder.
type Candidate struct {
	Path string // absolute
	Size int64  // allocated bytes, like du; each hard-linked file counted once per run
}

// Scan finds the target folders under root, which must already be resolved
// and passed by CheckRoot. Targets that may hold real files (see keep) are
// not candidates; their paths are returned as kept. It never follows symlinks, never enters .git or
// *.app folders, never crosses onto another volume or mount (like
// find -xdev), and does not look inside a match. Results are sorted by size
// (biggest first), then path.
// progress may be nil.
func Scan(root string, t Targets, workers int, progress *Progress) (cands []Candidate, kept []string) {
	workers = max(workers, 1)
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, nil
	}
	found := make(chan string)
	go func() {
		find(root, dev(fi), t, workers, found, progress)
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
				if !vetPath(root, p, t) {
					continue
				}
				if keep(p) {
					mu.Lock()
					kept = append(kept, p)
					mu.Unlock()
					continue
				}
				m := measure(p, progress)
				if m.otherFS {
					continue // a mount inside the target: deleting would reach into it
				}
				if progress != nil && progress.OnFound != nil {
					size := m.own
					for _, b := range m.shared {
						size += b
					}
					progress.OnFound(Candidate{Path: m.path, Size: size})
				}
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
	cands = make([]Candidate, 0, len(results))
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
	slices.Sort(kept)
	return cands, kept
}

// find walks root and sends every folder that matches a target. A subfolder
// gets its own goroutine while one of the workers slots is free, otherwise
// it's walked inline, so there are at most workers+1 walkers and nothing
// ever waits for a slot.
func find(root string, rootDev uint64, t Targets, workers int, found chan<- string, progress *Progress) {
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var walk func(dir string)
	walk = func(dir string) {
		if progress != nil {
			progress.dirs.Add(1)
			progress.current.Store(&dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return // ponytail: unreadable folders are skipped silently, like fd
		}
		if slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == IgnoreFile }) {
			return // the user marked this folder: never look inside
		}
		for _, e := range entries {
			// DirEntry types come from lstat: a symlink to a folder is not a dir here.
			if !e.IsDir() || e.Name() == ".git" || isApp(e.Name()) {
				continue
			}
			if info, err := e.Info(); err != nil || dev(info) != rootDev {
				continue // a mount point: network share, disk image, other volume
			}
			p := filepath.Join(dir, e.Name())
			if t.Match(p) {
				found <- p
				continue // prune: never look inside a match
			}
			select {
			case sem <- struct{}{}:
				wg.Go(func() {
					defer func() { <-sem }()
					walk(p)
				})
			default:
				walk(p)
			}
		}
	}
	walk(root)
	wg.Wait()
}

// IgnoreFile marks a folder sweep must never look inside.
const IgnoreFile = ".sweepignore"

// Vet reports whether p is safe to delete right now: strictly inside root,
// reached without passing through a symlink or a .git / *.app folder, not in
// or under a folder marked with IgnoreFile, a real directory (not a symlink)
// on the same volume as root, still a target, and not one to keep (see
// keep).
func Vet(root, p string, t Targets) bool {
	return vetPath(root, p, t) && !keep(p)
}

// vetPath is Vet without keep.
func vetPath(root, p string, t Targets) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	dir := root
	for _, c := range components(rel) {
		if c == ".git" || isApp(c) {
			return false
		}
		dir = filepath.Join(dir, c)
		if _, err := os.Lstat(filepath.Join(dir, IgnoreFile)); err == nil {
			return false
		}
	}
	if _, err := os.Lstat(filepath.Join(root, IgnoreFile)); err == nil {
		return false
	}
	// Lstat only checks p itself; a parent swapped for a symlink would
	// otherwise lead outside root.
	parent := filepath.Dir(p)
	if real, err := filepath.EvalSymlinks(parent); err != nil || real != parent {
		return false
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() || !t.Match(p) {
		return false
	}
	rfi, err := os.Lstat(root)
	return err == nil && dev(rfi) == dev(fi)
}

func dev(fi os.FileInfo) uint64 {
	return uint64(fi.Sys().(*syscall.Stat_t).Dev)
}

type inode struct{ dev, ino uint64 }

type measured struct {
	path    string
	own     int64           // blocks of everything not hard-linked
	shared  map[inode]int64 // blocks of hard-linked files, by inode
	otherFS bool            // something inside is on another volume
}

// measure adds up allocated blocks under p like `du -sk`, without
// following symlinks, and notes whether anything is mounted inside. Each
// folder it enters counts towards progress (may be nil), so a scan doesn't
// look stuck while big node_modules are measured.
func measure(p string, progress *Progress) measured {
	m := measured{path: p, shared: map[inode]int64{}}
	fi, err := os.Lstat(p)
	if err != nil {
		return m
	}
	pDev := dev(fi)
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // count what can be read
		}
		if progress != nil && d.IsDir() {
			progress.dirs.Add(1)
			progress.current.Store(&path)
		}
		info, err := d.Info() // lstat
		if err != nil {
			return nil
		}
		st := info.Sys().(*syscall.Stat_t)
		if uint64(st.Dev) != pDev {
			m.otherFS = true
			return filepath.SkipAll
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
