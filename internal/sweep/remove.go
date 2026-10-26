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
// Deletion goes through r, so a folder swapped for a symlink after Vet can't
// lead outside root. ponytail: inside root, Root still follows relative
// symlinks, so that race could redirect a delete to another folder with a
// target name within root. Closing it needs O_NOFOLLOW at every level.
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
			if !Vet(root, c.Path, t) {
				return
			}
			rel, err := filepath.Rel(root, c.Path)
			if err != nil {
				return
			}
			beforeRemove(c.Path)
			if err := r.RemoveAll(rel); err != nil {
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
