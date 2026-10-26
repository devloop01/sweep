//go:build darwin && arm64

package main

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/devloop01/sweep/internal/sweep"
)

// The interactive screen: it opens at once and fills in while the scan
// runs. Move with the arrow keys, space selects, enter deletes the selected
// folders (or the highlighted one), q quits.

type state int

const (
	ready state = iota
	deleting
	deleted
	failed
)

type item struct {
	cand     sweep.Candidate
	path     string // relative to DIR, printable
	state    state
	selected bool
}

// screen is the interactive list, separate from the terminal so it can be
// tested with plain key names.
type screen struct {
	items     []item
	total     int64
	freed     int64
	skipped   int
	scanning  bool
	checked   int64 // folders the scan has looked at
	cursor    int
	top       int // first visible row
	page      int // visible rows, from the last view
	quitting  bool
	message   string
	deletions int // in progress
}

// add puts a folder the scan just found into the list, biggest first.
func (s *screen) add(c sweep.Candidate, path string) {
	s.items = append(s.items, item{cand: c, path: path})
	s.total += c.Size
	s.sort()
}

// finish takes the scan's final sizes (hard links counted once) and how
// many folders it kept.
func (s *screen) finish(cands []sweep.Candidate, kept int) {
	final := map[string]int64{}
	for _, c := range cands {
		final[c.Path] = c.Size
	}
	s.total = 0
	for i := range s.items {
		if size, ok := final[s.items[i].cand.Path]; ok && s.items[i].state != deleted {
			s.items[i].cand.Size = size
		}
		s.total += s.items[i].cand.Size
	}
	s.sort()
	s.scanning, s.skipped = false, kept
}

// sort orders the list biggest first. The cursor stays on the same folder,
// not the same row, so space never deletes a folder that just slid under it.
func (s *screen) sort() {
	var at string
	if len(s.items) > 1 {
		at = s.items[s.cursor].cand.Path
	}
	slices.SortStableFunc(s.items, func(a, b item) int {
		return cmp.Or(cmp.Compare(b.cand.Size, a.cand.Size), strings.Compare(a.cand.Path, b.cand.Path))
	})
	if at != "" {
		s.cursor = s.find(at)
	}
}

func (s *screen) find(path string) int {
	return slices.IndexFunc(s.items, func(it item) bool { return it.cand.Path == path })
}

// key applies one key. It returns the folders to delete, if any. The cursor
// never moves on its own, so a held-down enter deletes one folder.
func (s *screen) key(k string) (del []sweep.Candidate) {
	if k == "quit" {
		s.quitting = true
	}
	if len(s.items) == 0 {
		return nil
	}
	last := len(s.items) - 1
	switch k {
	case "up":
		s.cursor = max(s.cursor-1, 0)
	case "down":
		s.cursor = min(s.cursor+1, last)
	case "pgup":
		s.cursor = max(s.cursor-max(s.page, 1), 0)
	case "pgdn":
		s.cursor = min(s.cursor+max(s.page, 1), last)
	case "home":
		s.cursor = 0
	case "end":
		s.cursor = last
	case "space":
		if it := &s.items[s.cursor]; it.state == ready {
			it.selected = !it.selected
		}
	case "all":
		// Select every folder still there, or unselect all if they all are.
		all := !slices.ContainsFunc(s.items, func(it item) bool { return it.state == ready && !it.selected })
		for i := range s.items {
			s.items[i].selected = !all && s.items[i].state == ready
		}
	case "enter":
		if s.quitting {
			return nil
		}
		picked := slices.ContainsFunc(s.items, func(it item) bool { return it.selected })
		for i := range s.items {
			it := &s.items[i]
			if it.state == ready && (it.selected || !picked && i == s.cursor) {
				it.state, it.selected = deleting, false
				s.deletions++
				del = append(del, it.cand)
			}
		}
		if len(del) > 0 {
			s.message = ""
		}
	}
	return del
}

// done records how deleting the folder at path went. Folders are matched
// by path because the list may have been re-sorted meanwhile.
func (s *screen) done(path string, err error) {
	s.deletions--
	i := s.find(path)
	if err != nil {
		s.items[i].state = failed
		s.message = printable(err.Error())
		return
	}
	s.items[i].state = deleted
	s.freed += s.items[i].cand.Size
}

// finished reports whether the screen can close: quit was asked for and no
// deletion is still running.
func (s *screen) finished() bool { return s.quitting && s.deletions == 0 }

// view draws the screen for a w×h terminal, one string per line.
func (s *screen) view(e env, w, h int) []string {
	head := "sweep  " + folders(len(s.items)) + ", " + human(s.total)
	if s.scanning {
		head = fmt.Sprintf("sweep  scanning… %s checked, %s found", folders(int(s.checked)), folders(len(s.items)))
	}
	lines := []string{
		e.paint(bold, clip(head, w-1)),
		e.paint(dim, clip("↑↓ move   space select   a all   enter delete   q quit", w-1)),
		"",
	}
	s.page = max(h-len(lines)-2, 1)
	if s.cursor < s.top {
		s.top = s.cursor
	}
	if s.cursor >= s.top+s.page {
		s.top = s.cursor - s.page + 1
	}
	room := max(w-2-4-2-10-1, 1) // cursor, checkbox, gap, status column, spare
	for i := s.top; i < min(s.top+s.page, len(s.items)); i++ {
		it := s.items[i]
		mark, path := "  ", fit(it.path, room)
		if i == s.cursor {
			mark, path = "> ", e.paint(bold, path)
		}
		switch {
		case it.selected:
			mark += e.paint(yellow, "[x]") + " "
		case it.state == ready:
			mark += "[ ] "
		default:
			mark += "    "
		}
		status, style := human(it.cand.Size), sizeStyle(it.cand.Size)
		switch it.state {
		case deleting:
			status, style = "deleting…", yellow
		case deleted:
			status, style = "deleted", green
		case failed:
			status, style = "failed", red
		}
		pad := strings.Repeat(" ", max(room-utf8.RuneCountInString(fit(it.path, room)), 0))
		lines = append(lines, mark+path+pad+"  "+e.paint(style, fmt.Sprintf("%10s", status)))
	}
	if len(s.items) == 0 && s.scanning {
		lines = append(lines, e.paint(dim, clip("  Looking for folders to clean…", w-1)))
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	var nsel int
	var selSize int64
	for _, it := range s.items {
		if it.selected {
			nsel++
			selSize += it.cand.Size
		}
	}
	switch {
	case s.quitting && s.deletions > 0:
		lines = append(lines, e.paint(yellow, clip("Finishing deletions…", w-1)))
	case s.message != "":
		lines = append(lines, e.paint(red, clip(s.message, w-1)))
	case nsel > 0:
		lines = append(lines, e.paint(yellow, clip("Selected "+folders(nsel)+", "+human(selSize)+" · enter deletes them", w-1)))
	default:
		lines = append(lines, e.paint(green, clip("Freed "+human(s.freed), w-1)))
	}
	return lines
}

type result struct {
	path string
	err  error
}

// interactive scans root inside the screen, lets the user delete folders
// until they quit, then prints what was deleted. It returns the exit code.
func interactive(e env, r *os.Root, t sweep.Targets, root string) int {
	s := &screen{scanning: true}
	if err := runScreen(e, s, r, t, root); err != nil {
		fmt.Fprintln(e.stderr, "sweep:", err)
		return 1
	}

	var n, nfailed int
	for _, it := range s.items {
		switch it.state {
		case deleted:
			n++
		case failed:
			nfailed++
		}
	}
	if s.skipped > 0 {
		fmt.Fprintf(e.stdout, "Skipped %s that may hold real files: build folders outside git, or ones git tracks files in.\n", folders(s.skipped))
	}
	switch {
	case len(s.items) == 0 && !s.scanning:
		fmt.Fprintln(e.stdout, "Nothing to clean.")
	case n == 0 && nfailed == 0:
		fmt.Fprintln(e.stdout, "Nothing deleted.")
	default:
		fmt.Fprintln(e.stdout, e.paint(green, fmt.Sprintf("Deleted %s, freed about %s.", folders(n), human(s.freed))))
	}
	if nfailed > 0 {
		fmt.Fprintf(e.stderr, "sweep: %s couldn't be deleted (shown as failed).\n", folders(nfailed))
		return 1
	}
	return 0
}

type scanned struct {
	cands []sweep.Candidate
	kept  []string
}

// runScreen puts the terminal in raw mode on the alternate screen, scans in
// the background, runs the event loop, and always restores the terminal.
func runScreen(e env, s *screen, r *os.Root, t sweep.Targets, root string) error {
	if e.flushInput != nil {
		e.flushInput() // keys typed before the screen opened must not delete anything
	}
	if e.raw != nil {
		restore, err := e.raw()
		if err != nil {
			return err
		}
		defer restore()
	}
	fmt.Fprint(e.stdout, "\x1b[?1049h\x1b[?25l") // alternate screen, hide cursor
	defer fmt.Fprint(e.stdout, "\x1b[?25h\x1b[?1049l")

	done := make(chan struct{})
	defer close(done)

	// The scan runs in the background; each folder shows up when measured.
	found := make(chan sweep.Candidate)
	finished := make(chan scanned, 1)
	progress := &sweep.Progress{OnFound: func(c sweep.Candidate) {
		select {
		case found <- c:
		case <-done:
		}
	}}
	go func() {
		cands, kept := sweep.Scan(root, t, e.workers, progress)
		finished <- scanned{cands, kept}
	}()

	input := make(chan string)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := e.stdin.Read(buf)
			ks := keys(buf[:n])
			if err != nil {
				ks = append(ks, "quit")
			}
			for _, k := range ks {
				select {
				case input <- k:
				case <-done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// Resizes redraw; being closed or killed quits cleanly, so the terminal
	// is restored. (Ctrl-C arrives as a key: raw mode turns signals off.)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	tick := time.NewTicker(100 * time.Millisecond) // scan progress
	defer tick.Stop()
	results := make(chan result)
	sem := make(chan struct{}, max(e.workers, 1)) // deletions at once

	for {
		s.checked = progress.Dirs()
		w, h := e.size()
		fmt.Fprint(e.stdout, "\x1b[H"+strings.Join(s.view(e, w, h), "\x1b[K\n")+"\x1b[K\x1b[J")
		if s.finished() {
			return nil
		}
		select {
		case k := <-input:
			for _, c := range s.key(k) {
				go func() {
					sem <- struct{}{}
					defer func() { <-sem }()
					deleted, errs := sweep.Remove(r, t, []sweep.Candidate{c}, 1)
					var err error
					switch {
					case len(errs) > 0:
						err = errs[0]
					case len(deleted) == 0:
						rel, _ := filepath.Rel(root, c.Path)
						err = fmt.Errorf("kept %s: it changed since the scan", printable(rel))
					}
					results <- result{c.Path, err}
				}()
			}
		case c := <-found:
			rel, _ := filepath.Rel(root, c.Path)
			s.add(c, printable(rel))
		case f := <-finished:
			s.finish(f.cands, len(f.kept))
			if len(s.items) == 0 {
				s.key("quit") // nothing to clean: close and say so
			}
			if e.afterScan != nil {
				e.afterScan()
			}
		case res := <-results:
			s.done(res.path, res.err)
		case sig := <-sigs:
			if sig != syscall.SIGWINCH {
				s.key("quit")
			}
		case <-tick.C:
		}
	}
}

// clip cuts s to its first n runes.
func clip(s string, n int) string {
	r := []rune(s)
	return string(r[:min(len(r), max(n, 0))])
}

// fit cuts s from the left to n runes, keeping its end ("…/app/node_modules").
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

// keys turns raw terminal input into key names.
func keys(b []byte) []string {
	seqs := []struct{ in, key string }{
		{"\x1b[A", "up"}, {"\x1bOA", "up"}, {"\x1b[B", "down"}, {"\x1bOB", "down"},
		{"\x1b[5~", "pgup"}, {"\x1b[6~", "pgdn"},
		{"\x1b[H", "home"}, {"\x1bOH", "home"}, {"\x1b[1~", "home"},
		{"\x1b[F", "end"}, {"\x1bOF", "end"}, {"\x1b[4~", "end"},
	}
	single := map[byte]string{' ': "space", '\r': "enter", '\n': "enter", 'a': "all", 'k': "up", 'j': "down", 'g': "home", 'G': "end", 'q': "quit", 3: "quit", 4: "quit"}
	var out []string
next:
	for len(b) > 0 {
		for _, s := range seqs {
			if bytes.HasPrefix(b, []byte(s.in)) {
				out, b = append(out, s.key), b[len(s.in):]
				continue next
			}
		}
		if b[0] == 0x1b {
			if len(b) == 1 {
				return append(out, "quit") // Esc on its own
			}
			// An escape sequence we don't use: skip it whole.
			end := bytes.IndexFunc(b[1:], func(r rune) bool { return r >= '@' && r <= '~' && r != '[' && r != 'O' })
			if end < 0 {
				return out
			}
			b = b[end+2:]
			continue
		}
		if k, ok := single[b[0]]; ok {
			out = append(out, k)
		}
		b = b[1:]
	}
	return out
}

// makeRaw switches f to raw input (no echo, no line buffering, no signal
// keys) and returns a func that restores it. Output processing stays on.
func makeRaw(f *os.File) (restore func(), err error) {
	var old syscall.Termios
	if err := termios(f, syscall.TIOCGETA, &old); err != nil {
		return nil, err
	}
	raw := old
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Iflag &^= syscall.IXON | syscall.ICRNL | syscall.BRKINT | syscall.INPCK | syscall.ISTRIP
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	if err := termios(f, syscall.TIOCSETA, &raw); err != nil {
		return nil, err
	}
	return func() { termios(f, syscall.TIOCSETA, &old) }, nil
}

func termios(f *os.File, req uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}
