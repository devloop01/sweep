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
	"time"
)

// Progress lets another goroutine watch a running Scan.
type Progress struct {
	dirs    atomic.Int64
	current atomic.Pointer[string]
	stop    atomic.Bool

	// OnFound, if set, is called (from Scan's goroutines) with each folder as
	// soon as it's measured. Its Size counts files hard-linked into other
	// folders in full; Scan's result has the final sizes.
	OnFound func(Candidate)
}

// Stop makes the running Scan return soon, with the folders it has finished
// measuring. For a caller that no longer wants the rest.
func (p *Progress) Stop() { p.stop.Store(true) }

func (p *Progress) stopped() bool { return p != nil && p.stop.Load() }

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

	// Touched is the newest modification time of anything inside, so an
	// install or build shows up, like npkill's "last_mod".
	Touched time.Time

	fi os.FileInfo // the folder the scan saw; Remove deletes only that one
}

// Scan finds the target folders under root, which must already be resolved
// and passed by CheckRoot. Targets that may hold real files (see keep) are
// not candidates; their paths are returned as kept. It never follows symlinks, never enters .git,
// *.app or Trash folders, never crosses onto another volume or mount (like
// find -xdev), and does not look inside a match. Results are sorted by size
// (biggest first), then path.
// progress may be nil.
func Scan(root string, t Targets, workers int, progress *Progress) (cands []Candidate, kept []string) {
	workers = max(workers, 1)
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, nil
	}
	found := make(chan []string) // the matches in one folder
	go func() {
		find(root, dev(fi), t, workers, found, progress)
		close(found)
	}()

	var (
		mu      sync.Mutex
		results []measured
		vetting sync.WaitGroup
		wg      sync.WaitGroup
	)
	// One folder's matches are vetted together (one question to git for all
	// of them), then measured one by one, by whichever workers are free.
	vetted := make(chan string)
	rs := &repos{t: t} // asks each repository once, where keep asks per folder
	defer rs.close()
	for range workers {
		vetting.Go(func() {
			for ps := range found {
				if progress.stopped() {
					continue // drain
				}
				ps = slices.DeleteFunc(ps, func(p string) bool { return !vetPath(root, p, t) })
				for i, k := range rs.keepAll(ps) {
					if k {
						mu.Lock()
						kept = append(kept, ps[i])
						mu.Unlock()
						continue
					}
					vetted <- ps[i]
				}
			}
		})
	}
	go func() {
		vetting.Wait()
		close(vetted)
	}()
	for range workers {
		wg.Go(func() {
			for p := range vetted {
				if progress.stopped() {
					continue // drain
				}
				m := measure(p, progress)
				if m.fi == nil {
					continue // gone or unreadable since it was found: Remove would refuse it
				}
				if progress.stopped() {
					continue // not measured to the end
				}
				if m.otherFS {
					continue // a mount inside the target: deleting would reach into it
				}
				if progress != nil && progress.OnFound != nil {
					size := m.own
					for _, l := range m.shared {
						size += l.bytes
					}
					progress.OnFound(Candidate{Path: m.path, Size: size, Touched: m.touched, fi: m.fi})
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
	// All the links in one list, sorted so each file's first target comes
	// first: 24 bytes a link, where a set of the files seen costs twice that.
	type owned struct {
		link
		target int32 // index in results
	}
	n := 0
	for _, m := range results {
		n += len(m.shared)
	}
	links := make([]owned, 0, n)
	sizes := make([]int64, len(results))
	for i := range results {
		sizes[i] = results[i].own
		for _, l := range results[i].shared {
			links = append(links, owned{l, int32(i)})
		}
		results[i].shared = nil
	}
	slices.SortFunc(links, func(a, b owned) int { return cmp.Or(cmp.Compare(a.ino, b.ino), cmp.Compare(a.target, b.target)) })
	for i, l := range links {
		if i == 0 || links[i-1].ino != l.ino {
			sizes[l.target] += l.bytes
		}
	}
	cands = make([]Candidate, 0, len(results))
	for i, m := range results {
		cands = append(cands, Candidate{Path: m.path, Size: sizes[i], Touched: m.touched, fi: m.fi})
	}
	slices.SortFunc(cands, func(a, b Candidate) int {
		return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Path, b.Path))
	})
	slices.Sort(kept)
	return cands, kept
}

// find walks root and sends the folders that match a target, a folder's
// matches together. A subfolder
// gets its own goroutine while one of the workers slots is free, otherwise
// it's walked inline, so there are at most workers+1 walkers and nothing
// ever waits for a slot.
func find(root string, rootDev uint64, t Targets, workers int, found chan<- []string, progress *Progress) {
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var walk func(dir string)
	walk = func(dir string) {
		if progress.stopped() {
			return
		}
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
		var matches []string
		for _, e := range entries {
			// DirEntry types come from lstat: a symlink to a folder is not a dir here.
			if !e.IsDir() || skipped(e.Name()) {
				continue
			}
			if info, err := e.Info(); err != nil || dev(info) != rootDev {
				continue // a mount point: network share, disk image, other volume
			}
			p := filepath.Join(dir, e.Name())
			if t.Match(p) {
				matches = append(matches, p)
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
		if len(matches) > 0 {
			found <- matches
		}
	}
	walk(root)
	wg.Wait()
}

// skipped reports whether sweep never looks inside folders named name:
// .git, *.app bundles, and Trash folders (a disk's .Trashes, ~/.Trash), so
// what's already in the Trash is never listed again.
func skipped(name string) bool {
	return name == ".git" || isApp(name) || name == ".Trashes" || name == ".Trash"
}

// IgnoreFile marks a folder sweep must never look inside.
const IgnoreFile = ".sweepignore"

// Vet reports whether p is safe to delete right now: strictly inside root,
// reached without passing through a symlink or a .git / *.app / Trash folder, not in
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
		if skipped(c) {
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

// link is a hard-linked file: its inode number and allocated bytes. The
// device isn't kept: everything in a target is on root's volume.
type link struct {
	ino   uint64
	bytes int64
}

type measured struct {
	path    string
	own     int64       // blocks of everything not hard-linked
	shared  []link      // the hard-linked files, each once, sorted by inode
	otherFS bool        // something inside is on another volume
	touched time.Time   // newest mtime inside
	fi      os.FileInfo // Lstat of path
}

// measure adds up allocated blocks under p like `du -sk`, without
// following symlinks, and notes whether anything is mounted inside. Each
// folder it enters counts towards progress (may be nil), so a scan doesn't
// look stuck while big node_modules are measured.
func measure(p string, progress *Progress) measured {
	m := measured{path: p}
	fi, err := os.Lstat(p)
	if err != nil {
		return m
	}
	m.fi = fi
	pDev := dev(fi)
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // count what can be read
		}
		if progress.stopped() {
			return filepath.SkipAll
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
		if t := info.ModTime(); t.After(m.touched) {
			m.touched = t
		}
		b := st.Blocks * 512
		if !d.IsDir() && st.Nlink > 1 {
			m.shared = append(m.shared, link{uint64(st.Ino), b})
		} else {
			m.own += b
		}
		return nil
	})
	// A slice, not a map: pnpm hard-links every file it installs, and a
	// scan can see millions. This is 16 bytes a file; a map was about 55.
	slices.SortFunc(m.shared, func(a, b link) int { return cmp.Compare(a.ino, b.ino) })
	m.shared = slices.Clone(slices.CompactFunc(m.shared, func(a, b link) bool { return a.ino == b.ino })) // Clone: no spare capacity
	return m
}

// mountedInside reports whether anything under p, a folder on device pDev,
// is on another volume.
func mountedInside(p string, pDev uint64) bool {
	other := false
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // what can't be read can't be deleted either
		}
		if info, err := d.Info(); err == nil && dev(info) != pDev {
			other = true
			return filepath.SkipAll
		}
		return nil
	})
	return other
}
