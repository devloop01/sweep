//go:build darwin

package sweep

import (
	"os"
	"sync"
)

// Remove deletes each candidate that still passes Vet, with up to workers
// deletions at once. Candidates that no longer pass are dropped silently.
// It returns the candidates actually deleted (in input order) and one error
// per failed deletion.
//
// ponytail: a swap that lands between Vet and RemoveAll is not caught (a
// window of a few syscalls). RemoveAll itself never follows symlinks inside
// the tree. Closing the window needs openat/O_NOFOLLOW removal from root.
func Remove(root string, t Targets, cands []Candidate, workers int) (deleted []Candidate, errs []error) {
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
			if err := os.RemoveAll(c.Path); err != nil {
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
