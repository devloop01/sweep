//go:build darwin && arm64

package sweep

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Open opens root (already resolved and guarded) for Remove. Open it before
// scanning: from then on, deletion can't leave this folder even if its path
// is later swapped for a symlink.
func Open(root string) (*os.Root, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	opened, err1 := r.Stat(".")
	checked, err2 := os.Lstat(root)
	if err1 != nil || err2 != nil || !os.SameFile(opened, checked) {
		r.Close()
		return nil, fmt.Errorf("%s changed while opening it", root)
	}
	return r, nil
}

// beforeRemove lets tests change the tree between the last Vet and the delete.
var beforeRemove = func(path string) {}

// Remove deletes each candidate that still passes Vet, with up to workers
// deletions at once. Candidates that no longer pass are dropped silently.
// It returns the candidates actually deleted (in input order) and one error
// per failed deletion.
//
// A folder can be swapped between Vet and the delete. So the delete opens
// the target's parent (through r, so never outside root), holds it open, and
// deletes the target only if it's the very folder the scan saw (same device
// and inode). A symlink or rename anywhere on the path leads to some other
// folder, which is refused. ponytail: what's left is a different folder
// swapped into the target's exact place between that check and the delete;
// renaming it aside first and checking again would close that. Candidates
// not from Scan (no scanned folder to compare) are refused.
func Remove(r *os.Root, t Targets, cands []Candidate, workers int) (deleted []Candidate, errs []error) {
	root := r.Name()
	done := make([]bool, len(cands))
	failed := make([]error, len(cands))
	sem := make(chan struct{}, max(workers, 1))
	var wg sync.WaitGroup
	for i, c := range cands {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if c.fi == nil || !Vet(root, c.Path, t) {
				return
			}
			rel, err := filepath.Rel(root, c.Path)
			if err != nil {
				return
			}
			beforeRemove(c.Path)
			parent, err := r.OpenRoot(filepath.Dir(rel))
			if err != nil {
				return
			}
			defer parent.Close()
			name := filepath.Base(rel)
			if fi, err := parent.Lstat(name); err != nil || !fi.IsDir() || !os.SameFile(fi, c.fi) {
				return
			}
			if err := parent.RemoveAll(name); err != nil {
				failed[i] = err
				return
			}
			done[i] = true
		})
	}
	wg.Wait()
	for i, c := range cands {
		if done[i] {
			deleted = append(deleted, c)
		}
		if failed[i] != nil {
			errs = append(errs, failed[i])
		}
	}
	return deleted, errs
}
