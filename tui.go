//go:build darwin && arm64

package main

import (
	"cmp"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/devloop01/sweep/internal/sweep"
)

// The interactive screen: it opens at once and fills in while the scan
// runs. Move with the arrow keys, space selects, enter deletes the selected
// folders (or the highlighted one), q quits. screen holds the list and its
// rules; Bubble Tea (model, below) runs it in the terminal.

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
	deletions int       // in progress
	moved     bool      // a key was pressed; until then the cursor stays on the top row
	topSince  time.Time // when the top row last changed while the cursor sat there
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

// sort orders the list biggest first. Once a key has been pressed the
// cursor stays on the same folder, not the same row, so space never deletes
// a folder that just slid under it. Before that it stays on the top row.
func (s *screen) sort() {
	var at string
	if len(s.items) > 1 {
		at = s.items[s.cursor].cand.Path
	}
	slices.SortStableFunc(s.items, func(a, b item) int {
		return cmp.Or(cmp.Compare(b.cand.Size, a.cand.Size), strings.Compare(a.cand.Path, b.cand.Path))
	})
	switch {
	case len(s.items) == 0:
	case !s.moved:
		if s.items[0].cand.Path != at {
			s.topSince = time.Now()
		}
	case at != "":
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
	if !s.moved {
		s.moved = true
		// The top row just changed: this key may have been meant for the
		// folder that was there. It only stops the cursor following the top.
		if (k == "space" || k == "enter") && time.Since(s.topSince) < 500*time.Millisecond {
			return nil
		}
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

// look is what view needs from the terminal beyond its size: whether the
// background is dark, and the spinner's current frame.
type look struct {
	dark bool
	spin string
}

var (
	accent = lipgloss.Color("#7D56F4")
	badge  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFDF5")).Background(accent).Padding(0, 1)
	faint  = lipgloss.NewStyle().Faint(true)
	strong = lipgloss.NewStyle().Bold(true)
	warn   = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	good   = lipgloss.NewStyle().Foreground(lipgloss.Green)
	bad    = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

// sizeLook is sizeStyle (shared with the plain list) as a Lip Gloss style.
func sizeLook(b int64) lipgloss.Style {
	st, code := lipgloss.NewStyle(), sizeStyle(b)
	switch {
	case strings.Contains(code, red):
		st = st.Foreground(lipgloss.Red)
	case strings.Contains(code, yellow):
		st = st.Foreground(lipgloss.Yellow)
	case strings.Contains(code, dim):
		st = st.Faint(true)
	}
	return st.Bold(strings.Contains(code, bold))
}

// view draws the screen for a w×h terminal, one string per line: a title,
// the list, a status line, and the keys that do something right now.
func (s *screen) view(l look, w, h int) []string {
	head := strong.Render(folders(len(s.items)) + " · " + human(s.total))
	if s.scanning {
		head = l.spin + " " + faint.Render(fmt.Sprintf("Scanning… %s checked · %s found", folders(int(s.checked)), folders(len(s.items))))
	}
	lines := []string{clip(badge.Render("sweep")+"  "+head, w-1), ""}
	s.page = max(h-len(lines)-3, 1)
	if s.cursor < s.top {
		s.top = s.cursor
	}
	if s.cursor >= s.top+s.page {
		s.top = s.cursor - s.page + 1
	}
	ageW := 0 // "3mo ago" column plus its gap, only where there's room for it
	if w >= 50 {
		ageW = 10
	}
	room := max(w-4-2-ageW-10-1, 1) // cursor bar and checkbox, gap, age, status column, spare
	hl := lipgloss.NewStyle().Background(lipgloss.LightDark(l.dark)(lipgloss.Color("#E8E4FA"), lipgloss.Color("#2B2540")))
	for i := s.top; i < min(s.top+s.page, len(s.items)); i++ {
		it := s.items[i]
		row := lipgloss.NewStyle()
		bar, pathStyle := " ", lipgloss.NewStyle()
		if i == s.cursor {
			row, bar, pathStyle = hl, lipgloss.NewStyle().Foreground(accent).Render("▌"), strong
		}
		box := " "
		switch {
		case it.selected:
			box = lipgloss.NewStyle().Foreground(accent).Bold(true).Inherit(row).Render("◉")
		case it.state == ready:
			box = faint.Inherit(row).Render("○")
		}
		status, style := human(it.cand.Size), sizeLook(it.cand.Size)
		switch it.state {
		case deleting:
			status, style = l.spin+" deleting", warn
		case deleted:
			status, style = "✓ deleted", good
		case failed:
			status, style = "✗ failed", bad
		}
		path := fit(it.path, room)
		cells := pathStyle.Inherit(row).Render(path) + row.Render(strings.Repeat(" ", max(room-ansi.StringWidth(path), 0))+"  ")
		if ageW > 0 {
			cells += faint.Inherit(row).Render(fmt.Sprintf("%8s", age(it.cand.Touched, time.Now()))) + row.Render("  ")
		}
		pad := strings.Repeat(" ", max(10-ansi.StringWidth(status), 0))
		lines = append(lines, bar+row.Render(" ")+box+row.Render(" ")+cells+style.Inherit(row).Render(pad+status)+row.Render(" "))
	}
	if len(s.items) == 0 && s.scanning {
		lines = append(lines, faint.Render(clip("  Looking for folders to clean…", w-1)))
	}
	for len(lines) < h-2 {
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
		lines = append(lines, clip(l.spin+" "+warn.Render("Finishing deletions…"), w-1))
	case s.message != "":
		lines = append(lines, clip(bad.Render(s.message), w-1))
	case nsel > 0:
		lines = append(lines, clip(warn.Render("Selected "+folders(nsel)+", "+human(selSize)), w-1))
	default:
		lines = append(lines, clip(good.Render("Freed "+human(s.freed)), w-1))
	}
	return append(lines, s.help(l, w))
}

// help is the key bar, showing only keys that do something right now.
func (s *screen) help(l look, w int) string {
	var here *item
	if len(s.items) > 0 {
		here = &s.items[s.cursor]
	}
	canSelect := here != nil && here.state == ready
	nsel := 0
	for _, it := range s.items {
		if it.selected {
			nsel++
		}
	}
	// Keys come from keyNames; help hides a binding that's switched off.
	bind := func(k, desc string, on bool, names ...string) key.Binding {
		var keys []string
		for kn, n := range keyNames {
			if slices.Contains(names, n) {
				keys = append(keys, kn)
			}
		}
		b := key.NewBinding(key.WithKeys(keys...), key.WithHelp(k, desc))
		b.SetEnabled(on)
		return b
	}
	delDesc := "delete"
	if nsel > 0 {
		delDesc = "delete " + folders(nsel)
	}
	hm := help.New()
	hm.Styles = help.DefaultStyles(l.dark)
	hm.SetWidth(w - 1)
	return hm.ShortHelpView([]key.Binding{
		bind("↑↓", "move", len(s.items) > 1, "up", "down"),
		bind("space", "select", canSelect, "space"),
		bind("a", "all", slices.ContainsFunc(s.items, func(it item) bool { return it.state == ready }), "all"),
		bind("enter", delDesc, !s.quitting && (nsel > 0 || canSelect), "enter"),
		bind("q", "quit", true, "quit"),
	})
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

// Messages the scan, deletions and signals send to the screen.
type (
	foundMsg struct {
		cand sweep.Candidate
		path string
	}
	scanDoneMsg struct {
		cands []sweep.Candidate
		kept  int
	}
	signalMsg struct{}
)

// model runs the screen inside Bubble Tea, which owns the terminal: raw
// input, the alternate screen, resizes, and restoring it all on exit.
type model struct {
	s        *screen
	e        env
	w, h     int
	progress *sweep.Progress
	remove   func(sweep.Candidate) result
	spin     spinner.Model
	spinning bool // a spinner tick is pending
	dark     bool // the terminal's background, once it has said
}

// keyNames maps Bubble Tea's key names to the screen's.
var keyNames = map[string]string{
	"up": "up", "k": "up", "down": "down", "j": "down",
	"pgup": "pgup", "pgdown": "pgdn", "home": "home", "g": "home", "end": "end", "G": "end",
	"space": "space", "enter": "enter", "a": "all",
	"q": "quit", "esc": "quit", "ctrl+c": "quit", "ctrl+d": "quit",
}

func (m *model) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		k, ok := keyNames[msg.String()]
		if !ok {
			break // other keys do nothing, not even unpin the cursor
		}
		for _, c := range m.s.key(k) {
			cmds = append(cmds, func() tea.Msg { return m.remove(c) })
		}
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case foundMsg:
		m.s.add(msg.cand, msg.path)
	case scanDoneMsg:
		m.s.finish(msg.cands, msg.kept)
		if len(m.s.items) == 0 {
			m.s.key("quit") // nothing to clean: close and say so
		}
		if m.e.afterScan != nil {
			m.e.afterScan()
		}
	case result:
		m.s.done(msg.path, msg.err)
	case signalMsg:
		m.s.key("quit")
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
	case spinner.TickMsg:
		// The spinner runs while something is going on; each tick also
		// redraws the scan's folder count.
		m.spinning = false
		if m.s.scanning || m.s.deletions > 0 {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			cmds, m.spinning = append(cmds, cmd), true
		}
	}
	if !m.spinning && (m.s.scanning || m.s.deletions > 0) {
		cmds, m.spinning = append(cmds, m.spin.Tick), true
	}
	m.s.checked = m.progress.Dirs()
	if m.s.finished() {
		return m, tea.Quit
	}
	return m, tea.Batch(cmds...)
}

func (m *model) View() tea.View {
	v := tea.NewView(strings.Join(m.s.view(look{m.dark, m.spin.View()}, m.w, m.h), "\n"))
	v.AltScreen = true
	return v
}

// runScreen scans in the background and runs the screen until the user
// quits and the deletions they started have finished.
func runScreen(e env, s *screen, r *os.Root, t sweep.Targets, root string) error {
	if e.flushInput != nil {
		e.flushInput() // keys typed before the screen opened must not delete anything
	}
	w, h := e.size()
	sem := make(chan struct{}, max(e.workers, 1)) // deletions at once
	m := &model{s: s, e: e, w: w, h: h, dark: true, progress: &sweep.Progress{}, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(accent))), remove: func(c sweep.Candidate) result {
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
		return result{c.Path, err}
	}}
	// Bubble Tea's own handler quits at once on SIGTERM; sweep's quit waits
	// for running deletions. (Ctrl-C arrives as a key: input is raw.)
	p := tea.NewProgram(m, tea.WithInput(e.stdin), tea.WithOutput(e.stdout), tea.WithWindowSize(w, h), tea.WithoutSignalHandler())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		for range sigs {
			p.Send(signalMsg{})
		}
	}()

	// The scan runs in the background; each folder shows up when measured.
	// Send returns at once after the screen has closed.
	m.progress.OnFound = func(c sweep.Candidate) {
		rel, _ := filepath.Rel(root, c.Path)
		p.Send(foundMsg{c, printable(rel)})
	}
	go func() {
		cands, kept := sweep.Scan(root, t, e.workers, m.progress)
		p.Send(scanDoneMsg{cands, len(kept)})
	}()
	_, err := p.Run()
	return err
}

// age says how long ago t was, short enough for an 8-wide column.
func age(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	switch d := now.Sub(t); {
	case d < 24*time.Hour:
		return "today"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", d/(24*time.Hour))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", d/(30*24*time.Hour))
	default:
		return fmt.Sprintf("%dy ago", d/(365*24*time.Hour))
	}
}

// clip cuts s to n columns.
func clip(s string, n int) string { return ansi.Truncate(s, max(n, 0), "") }

// fit cuts s from the left to n columns, keeping its end ("…/app/node_modules").
func fit(s string, n int) string {
	if w := ansi.StringWidth(s); w > n {
		return "…" + ansi.TruncateLeft(s, w-n+1, "")
	}
	return s
}
