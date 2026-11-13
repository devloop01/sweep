//go:build darwin && arm64

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
// runs. Move with the arrow keys, space or enter selects, d deletes the
// selected folders (or the highlighted one) after asking, q quits. screen holds the list and its
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
	proj     *project // nil outside any project
	sub      string   // path inside the project, printable
}

// layout is where view drew what clicks can hit, by line and column.
type layout struct {
	head     int       // line of the column names, -1 if none
	cols     [3][2]int // columns of Folder, Touched, Size: [from, to)
	list     int       // line of the first list row
	shown    []string  // rowID of each list row drawn, top to bottom
	buttons  int       // line of the dialog's buttons, -1 if none
	del, can [2]int    // columns of Delete and Cancel: [from, to)
}

// savedView is what the screen remembers between runs: the project view and the
// sort order. The cursor and folded projects depend on what's on disk, so
// they start fresh.
type savedView struct {
	Projects bool   `json:"projects"`
	Sort     string `json:"sort"`
}

var orderNames = []string{bySize: "size", byName: "name", byRecent: "recent"}

func viewOf(s *screen) savedView { return savedView{s.tree, orderNames[s.order]} }

// loadView applies the view saved at path, if any, and returns it. A missing
// or broken file means the defaults.
func loadView(path string, s *screen) savedView {
	if path == "" {
		return viewOf(s)
	}
	var v savedView
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &v) == nil {
		s.tree = v.Projects
		if i := slices.Index(orderNames, v.Sort); i >= 0 {
			s.order = order(i)
		}
	}
	return viewOf(s)
}

// saveView writes v to path. Failing only means it's not remembered.
func saveView(path string, v savedView) {
	if path == "" {
		return
	}
	b, _ := json.Marshal(v)
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		os.WriteFile(path, append(b, '\n'), 0o644)
	}
}

// order is what the list is sorted by. s cycles through them in the
// columns' order, left to right: Folder, Touched, then back to Size.
type order int

const (
	bySize   order = iota // biggest first
	byName                // A to Z
	byRecent              // most recently changed first
)

// project is the project a folder belongs to: a monorepo, or one app.
type project struct {
	sweep.Project
	name string // relative to DIR, printable
}

// group is what the list sorts and groups folders by: the project, when
// the screen shows projects.
func (s *screen) group(it item) string {
	if s.tree && it.proj != nil {
		return "\x00" + it.proj.Dir
	}
	return it.cand.Path
}

// row is one line of the list: a folder, items[i], or the name of a project
// with more than one folder, which are items[i:i+n]. Header rows aren't
// folders and are never deleted: space and d on one act on its folders,
// enter collapses or opens it.
type row struct {
	i, n        int
	head        bool
	child, last bool // the folder is listed under its project's name
}

// screen is the interactive list, separate from the terminal so it can be
// tested with plain key names.
type screen struct {
	items     []item // folders, in list order
	rows      []row  // the list as shown, with project names; cursor and top index it
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
	deletions int             // in progress
	moved     bool            // a key was pressed; until then the cursor stays on the top row
	topSince  time.Time       // when the top row last changed while the cursor sat there
	ask       []string        // folders the confirm dialog asks about; nil when it's closed
	tree      bool            // folders are listed under their project (t)
	order     order           // what the list is sorted by (s)
	folded    map[string]bool // projects collapsed to one row, by folder
	helping   bool            // the key list (?) is open
	reveal    string          // a folder to show in Finder (o), for the model to open
	drawn     layout          // where the last frame put things, for clicks
	lastClick struct {        // the last click on a row, to spot a double-click
		row string
		at  time.Time
	}
	free int64 // free space on DIR's disk, 0 if unknown
	yes  bool  // the dialog's Delete button has focus (Cancel does at first)
}

// add puts a folder the scan just found into the list, biggest first.
func (s *screen) add(c sweep.Candidate, path string) { s.addIn(c, path, nil) }

// addIn adds a folder that belongs to project p, listed under its name.
func (s *screen) addIn(c sweep.Candidate, path string, p *project) {
	it := item{cand: c, path: path, proj: p}
	if p != nil {
		rel, _ := filepath.Rel(p.Dir, c.Path)
		it.sub = printable(rel)
	}
	s.items = append(s.items, it)
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

// sort orders the list biggest first, each project's folders together
// under its name. Once a key has been pressed the cursor stays on the same
// row, not the same line, so space never deletes a folder that just slid
// under it. Before that it stays on the top line.
func (s *screen) sort() {
	var at string
	if len(s.rows) > 0 {
		at = s.rowID(s.rows[s.cursor])
	}
	// A project sorts by its folders' total size or newest change.
	sum, newest, n := map[string]int64{}, map[string]time.Time{}, map[string]int{}
	for _, it := range s.items {
		g := s.group(it)
		sum[g] += it.cand.Size
		if it.cand.Touched.After(newest[g]) {
			newest[g] = it.cand.Touched
		}
		n[g]++
	}
	slices.SortStableFunc(s.items, func(a, b item) int {
		ga, gb := s.group(a), s.group(b)
		var byGroup, byFolder int
		switch s.order {
		case bySize:
			byGroup, byFolder = cmp.Compare(sum[gb], sum[ga]), cmp.Compare(b.cand.Size, a.cand.Size)
		case byRecent:
			byGroup, byFolder = newest[gb].Compare(newest[ga]), b.cand.Touched.Compare(a.cand.Touched)
		}
		// Then by name: a project's folder, else the folder itself.
		return cmp.Or(byGroup, strings.Compare(strings.TrimPrefix(ga, "\x00"), strings.TrimPrefix(gb, "\x00")),
			byFolder, strings.Compare(a.cand.Path, b.cand.Path))
	})
	s.rows = s.rows[:0]
	for i, it := range s.items {
		g := s.group(it)
		many := n[g] > 1
		if many && (i == 0 || s.group(s.items[i-1]) != g) {
			s.rows = append(s.rows, row{i: i, n: n[g], head: true})
		}
		if many && s.folded[it.proj.Dir] {
			continue
		}
		s.rows = append(s.rows, row{i: i, n: 1, child: many, last: many && (i+1 == len(s.items) || s.group(s.items[i+1]) != g)})
	}
	switch {
	case len(s.rows) == 0:
	case !s.moved:
		if s.rowID(s.rows[0]) != at {
			s.topSince = time.Now()
		}
	case at != "":
		s.cursor = max(slices.IndexFunc(s.rows, func(r row) bool { return s.rowID(r) == at }), 0)
	}
}

// rowID names a row so the cursor can find it again after a re-sort.
func (s *screen) rowID(r row) string {
	if r.head {
		return "project:" + s.items[r.i].proj.Dir
	}
	return s.items[r.i].cand.Path
}

// under is the folders on the cursor's row: one, or a project's.
func (s *screen) under() []int {
	r := s.rows[s.cursor]
	var is []int
	for i := r.i; i < r.i+r.n; i++ {
		is = append(is, i)
	}
	return is
}

func (s *screen) find(path string) int {
	return slices.IndexFunc(s.items, func(it item) bool { return it.cand.Path == path })
}

// key applies one key. It returns the folders to delete, if any; only
// answering the confirm dialog does. The cursor never moves on its own.
func (s *screen) key(k string) (del []sweep.Candidate) {
	if k == "interrupt" {
		// ctrl+c and ctrl+d quit from anywhere, the key list included; q
		// there is "any key", and only closes it.
		s.helping, k = false, "quit"
	}
	if s.helping {
		s.helping = false // any key closes the key list
		return nil
	}
	if s.ask != nil && k != "quit" {
		return s.answer(k)
	}
	if k == "help" {
		s.helping = true
		return nil
	}
	if k == "quit" || k == "esc" {
		s.quitting, s.ask = true, nil
	}
	if len(s.items) == 0 || k == "yes" || k == "no" || !s.tree && (k == "left" || k == "right" || k == "next" || k == "prev" || k == "fold") {
		return nil // keys that do nothing here must not unpin the cursor
	}
	if !s.moved {
		s.moved = true
		// The top row just changed: this key may have been meant for the
		// folder that was there. It only stops the cursor following the top.
		if (k == "space" || k == "enter" || k == "delete") && time.Since(s.topSince) < 500*time.Millisecond {
			return nil
		}
	}
	last := len(s.rows) - 1
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
		s.toggle()
	case "all":
		// Select every folder still there, or unselect all if they all are.
		all := !slices.ContainsFunc(s.items, func(it item) bool { return it.state == ready && !it.selected })
		for i := range s.items {
			s.items[i].selected = !all && s.items[i].state == ready
		}
	case "left", "right", "fold":
		// Collapse or open the cursor's project (c: every project). From a
		// folder inside it, the cursor moves up to the project's row.
		dir := ""
		if it := s.items[s.rows[s.cursor].i]; it.proj != nil && (s.rows[s.cursor].head || s.rows[s.cursor].child) {
			dir = it.proj.Dir
		}
		if s.folded == nil {
			s.folded = map[string]bool{}
		}
		switch {
		case k == "fold":
			// Collapse all, or open all if they all are.
			all := true
			for _, r := range s.rows {
				all = all && (!r.head || s.folded[s.items[r.i].proj.Dir])
			}
			for _, r := range s.rows {
				if r.head {
					s.folded[s.items[r.i].proj.Dir] = !all
				}
			}
			s.refold(dir)
		case dir != "":
			s.folded[dir] = k == "left"
			s.refold(dir)
		}
	case "next", "prev":
		// Jump to the next project's row, or back to the previous one (from
		// a folder inside a project, back to its row).
		step := 1
		if k == "prev" {
			step = -1
		}
		for i := s.cursor + step; i >= 0 && i < len(s.rows); i += step {
			if s.rows[i].head {
				s.cursor = i
				break
			}
		}
	case "sort":
		s.order = (s.order + 1) % 3
		s.sort()
	case "tree":
		// Stay on the same folder; from a project's row, its first one.
		at := s.items[s.rows[s.cursor].i].cand.Path
		s.tree = !s.tree
		s.sort()
		s.cursor = max(slices.IndexFunc(s.rows, func(r row) bool { return !r.head && s.rowID(r) == at }), 0)
	case "open":
		if r := s.rows[s.cursor]; r.head {
			s.reveal = s.items[r.i].proj.Dir
		} else {
			s.reveal = s.items[r.i].cand.Path
		}
	case "enter":
		// Acts on the row: a project collapses or opens, a folder is
		// selected like with space. It never deletes.
		if r := s.rows[s.cursor]; r.head {
			s.toggleFold(s.items[r.i].proj.Dir)
		} else {
			s.toggle()
		}
	case "delete":
		// Ask first. The dialog keeps the folders' paths, so a re-sort
		// while it's open can't change what gets deleted.
		if s.quitting {
			return nil
		}
		picked := slices.ContainsFunc(s.items, func(it item) bool { return it.selected })
		here := s.under()
		for i, it := range s.items {
			if it.state == ready && (it.selected || !picked && slices.Contains(here, i)) {
				s.ask = append(s.ask, it.cand.Path)
			}
		}
		s.yes = false
	}
	return nil
}

// answer applies a key while the confirm dialog is open. Cancel has focus
// first and other keys are ignored, so a held-down d leaves it open and
// deletes nothing.
func (s *screen) answer(k string) (del []sweep.Candidate) {
	switch k {
	case "left", "right", "next", "prev":
		s.yes = !s.yes
		return nil
	case "enter", "yes":
		if !s.yes && k == "enter" {
			break
		}
		for _, p := range s.ask {
			if it := &s.items[s.find(p)]; it.state == ready {
				it.state, it.selected = deleting, false
				s.deletions++
				del = append(del, it.cand)
			}
		}
		s.message = ""
	case "no", "esc":
	default:
		return nil // other keys leave the dialog open
	}
	s.ask = nil
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

// click applies a left click at column x, line y of the last frame: it
// presses a dialog button, sorts by a column, or moves the cursor to a row
// and ticks its checkbox or folds its project if that was hit. A row is
// found by what was drawn there, so a re-sort since can't change the folder.
func (s *screen) click(x, y int) (del []sweep.Candidate) {
	in := func(r [2]int) bool { return x >= r[0] && x < r[1] }
	d := s.drawn
	switch {
	case s.helping:
		s.helping = false
		return nil
	case s.ask != nil:
		if y == d.buttons && in(d.del) {
			s.yes = true
			return s.answer("enter")
		}
		if y == d.buttons && in(d.can) {
			return s.answer("no")
		}
		return nil
	case y == d.head:
		for i, o := range []order{byName, byRecent, bySize} {
			if in(d.cols[i]) && s.order != o {
				s.order, s.moved = o, true
				s.sort()
			}
		}
		return nil
	}
	if y < d.list || y >= d.list+len(d.shown) {
		return nil
	}
	ri := slices.IndexFunc(s.rows, func(r row) bool { return s.rowID(r) == d.shown[y-d.list] })
	if ri < 0 {
		return nil
	}
	s.cursor, s.moved = ri, true
	switch r := s.rows[ri]; {
	case r.head && x >= 8 && x < 10: // ▾ ▸
		s.toggleFold(s.items[r.i].proj.Dir)
		return nil
	case r.child && x >= 11 && x < 14, !r.child && x >= 3 && x < 6: // [ ]
		s.toggle()
		return nil
	}
	// A second click on the same row soon after selects it, like space.
	id, now := s.rowID(s.rows[ri]), time.Now()
	if s.lastClick.row == id && now.Sub(s.lastClick.at) < 400*time.Millisecond {
		s.lastClick.row = "" // a third click starts over
		s.toggle()
		return nil
	}
	s.lastClick.row, s.lastClick.at = id, now
	return nil
}

// toggle selects the cursor row's folders (one, or a project's), or
// unselects them if they all are.
func (s *screen) toggle() {
	all := !slices.ContainsFunc(s.under(), func(i int) bool { return s.items[i].state == ready && !s.items[i].selected })
	for _, i := range s.under() {
		s.items[i].selected = !all && s.items[i].state == ready
	}
}

// toggleFold collapses the project at dir, or opens it if it's collapsed.
func (s *screen) toggleFold(dir string) {
	if s.folded == nil {
		s.folded = map[string]bool{}
	}
	s.folded[dir] = !s.folded[dir]
	s.refold(dir)
}

// refold rebuilds the rows after projects were collapsed or opened. The
// cursor stays on its row, or moves up to the row of dir's project if that
// just collapsed under it.
func (s *screen) refold(dir string) {
	at := s.rowID(s.rows[s.cursor])
	if dir != "" && s.folded[dir] {
		at = "project:" + dir
	}
	s.sort()
	s.cursor = max(slices.IndexFunc(s.rows, func(r row) bool { return s.rowID(r) == at }), 0)
}

// quit closes the screen once deletions finish, whatever is open. It's for
// signals, end of input and an empty scan; the q key goes through key.
func (s *screen) quit() { s.quitting, s.ask, s.helping = true, nil, false }

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
		head = l.spin + " " + faint.Render(fmt.Sprintf("Scanning… %d checked, %d found", s.checked, len(s.items)))
	}
	// Free space goes last, and only if it fits whole.
	if free := " · " + human(s.free) + " free"; s.free > 0 && ansi.StringWidth(" sweep   "+head+free) < w {
		head += faint.Render(free)
	}
	// A one-column margin, and in a tall enough terminal a blank line on top
	// and the column names.
	roomy := h >= 12
	lines := []string{clip(" "+badge.Render("sweep")+"  "+head, w-1), ""}
	if roomy {
		lines = append([]string{""}, lines...)
	}
	s.page = max(h-len(lines)-3, 1) // a blank line, status, keys
	if roomy && len(s.items) > 0 {
		s.page = max(s.page-1, 1)
	}
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
	room := max(w-1-7-2-ageW-10-1, 1) // margin, cursor bar and checkbox, gap, age, status column, spare
	s.drawn = layout{head: -1, buttons: -1}
	if roomy && len(s.items) > 0 {
		s.drawn.head = len(lines)
		s.drawn.cols = [3][2]int{{8, 8 + room}, {8 + room + 2, 8 + room + 2 + ageW - 2}, {8 + room + 2 + ageW, 8 + room + 2 + ageW + 10}}
		// The column the list is sorted by is marked with an arrow.
		label := func(name string, o order, width int) string {
			if s.order == o {
				return lipgloss.NewStyle().Foreground(accent).Bold(true).Render(fmt.Sprintf("%*s", width, name+"↓"))
			}
			return faint.Render(fmt.Sprintf("%*s", width, name))
		}
		names := "        " + label("Folder", byName, -room) + "  "
		if ageW > 0 {
			names += label("Touched", byRecent, 8) + "  "
		}
		lines = append(lines, clip(names+label("Size", bySize, 10), w-1))
	}
	hl := lipgloss.NewStyle().Background(lipgloss.LightDark(l.dark)(lipgloss.Color("#E8E4FA"), lipgloss.Color("#2B2540")))
	s.drawn.list = len(lines)
	for ri := s.top; ri < min(s.top+s.page, len(s.rows)); ri++ {
		r := s.rows[ri]
		s.drawn.shown = append(s.drawn.shown, s.rowID(r))
		it := s.items[r.i]
		row := lipgloss.NewStyle()
		bar, pathStyle := " ", lipgloss.NewStyle()
		if ri == s.cursor {
			row, bar, pathStyle = hl, lipgloss.NewStyle().Foreground(accent).Render("▌"), strong
		}
		// A project's row sums up its folders.
		var size int64
		var touched time.Time
		var nready, nsel, ndeleting, nfailed int
		for _, f := range s.items[r.i : r.i+r.n] {
			if f.state != deleted {
				size += f.cand.Size
			}
			if f.cand.Touched.After(touched) {
				touched = f.cand.Touched
			}
			switch f.state {
			case ready:
				nready++
				if f.selected {
					nsel++
				}
			case deleting:
				ndeleting++
			case failed:
				nfailed++
			}
		}
		box := row.Render("   ")
		switch {
		case nsel > 0 && nsel == nready:
			box = lipgloss.NewStyle().Foreground(accent).Bold(true).Inherit(row).Render("[✓]")
			pathStyle = pathStyle.Foreground(accent)
		case nsel > 0:
			box = lipgloss.NewStyle().Foreground(accent).Bold(true).Inherit(row).Render("[-]")
		case nready > 0:
			box = faint.Inherit(row).Render("[ ]")
		}
		status, style := human(size), sizeLook(size)
		switch {
		case ndeleting > 0:
			status, style = l.spin+" deleting", warn
		case nready > 0:
		case nfailed > 0:
			status, style = "✗ failed", bad
		default:
			status, style = "✓ deleted", good
		}
		var cells string
		switch {
		case r.head:
			kind := "  " + it.proj.Kind
			arrow := "▾ "
			if s.folded[it.proj.Dir] {
				arrow = "▸ "
			}
			name := arrow + fit(it.proj.name, max(room-2-ansi.StringWidth(kind), 1))
			kind = ansi.Truncate(kind, room-ansi.StringWidth(name), "")
			cells = strong.Inherit(row).Render(name) + faint.Inherit(row).Render(kind) +
				row.Render(strings.Repeat(" ", max(room-ansi.StringWidth(name+kind), 0))+"  ")
		case r.child:
			branch := "├─ "
			if r.last {
				branch = "└─ "
			}
			// Indented under the project's name: branch, checkbox, path.
			path := fit(it.sub, max(room-7, 1))
			cells = faint.Inherit(row).Render(branch) + box + row.Render(" ") + pathStyle.Inherit(row).Render(path) +
				row.Render(strings.Repeat(" ", max(room-7-ansi.StringWidth(path), 0))+"  ")
			box = row.Render("   ")
		default:
			path := fit(it.path, room)
			cells = pathStyle.Inherit(row).Render(path) + row.Render(strings.Repeat(" ", max(room-ansi.StringWidth(path), 0))+"  ")
		}
		if ageW > 0 {
			cells += faint.Inherit(row).Render(fmt.Sprintf("%8s", age(touched, time.Now()))) + row.Render("  ")
		}
		pad := strings.Repeat(" ", max(10-ansi.StringWidth(status), 0))
		lines = append(lines, clip(" "+bar+row.Render(" ")+box+row.Render("  ")+cells+style.Inherit(row).Render(pad+status)+row.Render(" "), w-1))
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
		lines = append(lines, clip(" "+l.spin+" "+warn.Render("Finishing deletions…"), w-1))
	case s.message != "":
		lines = append(lines, clip(" "+bad.Render(s.message), w-1))
	case nsel > 0:
		lines = append(lines, clip(" "+warn.Render("Selected "+folders(nsel)+", "+human(selSize)), w-1))
	case s.freed > 0:
		lines = append(lines, clip(" "+good.Render("Freed "+human(s.freed)), w-1))
	default:
		lines = append(lines, "")
	}
	lines = append(lines, " "+s.help(l, w-1))
	var box string
	switch {
	case s.ask != nil:
		box = s.dialog(l, w)
	case s.helping:
		box = keyList(l)
	default:
		return lines
	}
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	bx, by := max((w-bw)/2, 0), max((h-bh)/2, 0)
	out := lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(lines, "\n")),
		lipgloss.NewLayer(box).X(bx).Y(by).Z(1),
	).Render()
	lines = strings.Split(out, "\n")
	for i := range lines {
		lines[i] = clip(lines[i], w-1)
	}
	// The buttons sit above the box's bottom padding and border. They're
	// found in the box itself, not the screen line, where list text beside
	// the box could also say "Delete".
	if y := by + bh - 3; s.ask != nil && y < min(len(lines), h) {
		plain := ansi.Strip(strings.Split(box, "\n")[bh-3])
		find := func(label string) [2]int {
			i := strings.LastIndex(plain, label)
			if i < 0 {
				return [2]int{}
			}
			x := bx + ansi.StringWidth(plain[:i])
			return [2]int{x - 2, x + len(label) + 2} // with the button's padding
		}
		s.drawn.buttons, s.drawn.del, s.drawn.can = y, find("Delete"), find("Cancel")
	}
	return lines[:min(len(lines), max(h, 1))]
}

// dialog is the confirm box drawn over the list.
func (s *screen) dialog(l look, w int) string {
	asked := map[string]bool{}
	for _, p := range s.ask {
		asked[p] = true
	}
	var size int64
	var first *item
	same := true // all in one project
	for i := range s.items {
		it := &s.items[i]
		if !asked[it.cand.Path] {
			continue
		}
		size += it.cand.Size
		if first == nil {
			first = it
		}
		same = same && it.proj != nil && first.proj != nil && it.proj.Dir == first.proj.Dir
	}
	what := folders(len(s.ask))
	switch {
	case len(s.ask) == 1:
		// The box adds about 28 columns around the path.
		what = fit(first.path, min(40, max(w-28, 8)))
	case same:
		what += " in " + fit(first.proj.name, min(30, max(w-40, 8)))
	}
	title := "Delete " + what + " (" + human(size) + ")?"
	button := func(label string, focus bool) string {
		st := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.LightDark(l.dark)(lipgloss.Color("#4A4A4A"), lipgloss.Color("#B2B2B2"))).
			Background(lipgloss.LightDark(l.dark)(lipgloss.Color("#E4E4E4"), lipgloss.Color("#3A3A3A")))
		if focus {
			st = st.Bold(true).Foreground(lipgloss.Color("#FFFDF5")).Background(accent)
		}
		return st.Render(label)
	}
	body := lipgloss.JoinVertical(lipgloss.Center,
		strong.Render(title),
		"",
		button("Delete", s.yes)+"  "+button("Cancel", !s.yes),
	)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 3).Render(body)
}

// keyList is the box ? opens: every key and what it does.
func keyList(l look) string {
	var b strings.Builder
	for i, k := range [][2]string{
		{"↑ ↓  k j", "move"},
		{"pgup pgdn", "move a page"},
		{"g G", "go to the top or bottom"},
		{"space", "select a folder, or all of a project's"},
		{"a", "select all, or none"},
		{"enter", "select a folder, or collapse or open a project"},
		{"d ⌫", "delete the selected folders, or the highlighted one"},
		{"s", "sort by size, name, or last change"},
		{"t", "group folders by project"},
		{"← → h l", "collapse or open a project"},
		{"tab ⇧tab", "next or previous project"},
		{"c", "collapse or open all projects"},
		{"o", "show the folder in Finder"},
		{"q esc", "quit"},
	} {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(lipgloss.NewStyle().Foreground(accent).Bold(true).Width(11).Render(k[0]) + k[1])
	}
	body := strong.Render("Keys") + "\n\n" + b.String() + "\n\n" + faint.Render("Press any key to close")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 3).Render(body)
}

// help is the key bar, showing only keys that do something right now.
func (s *screen) help(l look, w int) string {
	hm := help.New()
	hm.Styles = help.DefaultStyles(l.dark)
	hm.SetWidth(w - 1)
	if s.ask != nil {
		show := func(k, desc string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc)) }
		return hm.ShortHelpView([]key.Binding{show("←→", "choose"), show("enter", "confirm"), show("y", "delete"), show("n", "cancel")})
	}
	canSelect := len(s.rows) > 0 && slices.ContainsFunc(s.under(), func(i int) bool { return s.items[i].state == ready })
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
		delDesc = fmt.Sprintf("delete (%d)", nsel)
	}
	// Kept to fit 80 columns; moving and the rest are in the ? list.
	return clip(hm.ShortHelpView([]key.Binding{
		bind("space", "select", canSelect, "space"),
		// The project view shows c in a's place; there's room for one.
		bind("a", "all", !s.tree && slices.ContainsFunc(s.items, func(it item) bool { return it.state == ready }), "all"),
		bind("c", "fold all", s.tree && slices.ContainsFunc(s.rows, func(r row) bool { return r.head }), "fold"),
		bind("d", delDesc, !s.quitting && (nsel > 0 || canSelect), "delete"),
		bind("t", "projects", len(s.items) > 0, "tree"),
		bind("?", "help", true, "help"),
		bind("q", "quit", true, "quit"),
	}), w-1)
}

type result struct {
	path string
	err  error
	free int64 // the disk's free space after it, read off the UI thread
}

// interactive scans root inside the screen, lets the user delete folders
// until they quit, then prints what was deleted. It returns the exit code.
func interactive(e env, r *os.Root, t sweep.Targets, root string) int {
	s := &screen{scanning: true}
	before := loadView(e.statePath, s)
	defer func() {
		if v := viewOf(s); v != before {
			saveView(e.statePath, v)
		}
	}()
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
		fmt.Fprintln(e.stdout, skipped(s.skipped))
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
		fmt.Fprintf(e.stderr, "sweep: couldn't delete %s.\n", folders(nfailed))
		return 1
	}
	return 0
}

// Messages the scan, deletions and signals send to the screen.
type (
	foundMsg struct {
		cand sweep.Candidate
		path string
		proj *project
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
	answered bool // the background query was answered or timed out
}

// bgTimeout stops waiting for a terminal that never answers the query.
type bgTimeout struct{}

// keyNames maps Bubble Tea's key names to the screen's.
var keyNames = map[string]string{
	"up": "up", "k": "up", "down": "down", "j": "down",
	"pgup": "pgup", "pgdown": "pgdn", "home": "home", "g": "home", "end": "end", "G": "end",
	"space": "space", "enter": "enter", "a": "all", "d": "delete", "backspace": "delete", "delete": "delete",
	"q": "quit", "esc": "esc", "ctrl+c": "interrupt", "ctrl+d": "interrupt",
	"left": "left", "h": "left", "right": "right", "l": "right", "tab": "next", "shift+tab": "prev", "c": "fold", "s": "sort",
	"y": "yes", "n": "no", "t": "tree", "o": "open", "?": "help",
}

// Init asks for the background color. Quitting waits for the answer (up to
// a second, for a slow SSH link; only if it hasn't come yet), so it can't
// arrive after the terminal is restored and land in the shell.
func (m *model) Init() tea.Cmd {
	wait := cmp.Or(m.e.replyWait, time.Second)
	return tea.Batch(tea.RequestBackgroundColor, tea.Tick(wait, func(time.Time) tea.Msg { return bgTimeout{} }))
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		k, ok := keyNames[msg.String()]
		if !ok {
			m.s.helping = false // the key list closes on any key, as it says
			break               // other keys do nothing, not even unpin the cursor
		}
		for _, c := range m.s.key(k) {
			cmds = append(cmds, func() tea.Msg { return m.remove(c) })
		}
		if p := m.s.reveal; p != "" {
			m.s.reveal = ""
			cmds = append(cmds, func() tea.Msg { exec.Command("open", "-R", p).Run(); return nil })
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			for _, c := range m.s.click(msg.X, msg.Y) {
				cmds = append(cmds, func() tea.Msg { return m.remove(c) })
			}
		}
	case tea.MouseWheelMsg:
		if m.s.ask == nil && !m.s.helping {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.s.key("up")
			case tea.MouseWheelDown:
				m.s.key("down")
			}
		}
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case foundMsg:
		m.s.addIn(msg.cand, msg.path, msg.proj)
	case scanDoneMsg:
		m.s.finish(msg.cands, msg.kept)
		if len(m.s.items) == 0 {
			m.s.quit() // nothing to clean: close and say so
		}
		if m.e.afterScan != nil {
			m.e.afterScan()
		}
	case result:
		m.s.done(msg.path, msg.err)
		m.s.free = msg.free
	case signalMsg:
		m.s.quit()
	case tea.BackgroundColorMsg:
		m.dark, m.answered = msg.IsDark(), true
	case bgTimeout:
		m.answered = true
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
	if m.s.quitting {
		m.progress.Stop() // nobody is waiting for the rest of the scan
	}
	if m.s.finished() && m.answered {
		return m, tea.Quit
	}
	return m, tea.Batch(cmds...)
}

func (m *model) View() tea.View {
	v := tea.NewView(strings.Join(m.s.view(look{m.dark, m.spin.View()}, m.w, m.h), "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
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
	s.free = diskFree(root)
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
		return result{c.Path, err, diskFree(root)}
	}}
	// Bubble Tea's own handler quits at once on SIGTERM; sweep's quit waits
	// for running deletions. (Ctrl-C arrives as a key: input is raw.)
	// Bubble Tea stops reading quietly at end of input (a hung-up terminal
	// whose SIGHUP never came); make it a quit, which waits for deletions.
	var p *tea.Program
	quit := func() { p.Send(signalMsg{}) }
	var in io.Reader = quitOnEOF{e.stdin, quit}
	if f, ok := e.stdin.(*os.File); ok {
		in = quitOnEOFFile{f, quit} // still a file, so Bubble Tea sets raw mode
	}
	p = tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(e.stdout), tea.WithWindowSize(w, h), tea.WithoutSignalHandler())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP)
	defer func() {
		signal.Stop(sigs)
		close(sigs) // ends the goroutine below; nothing sends after Stop
	}()
	go func() {
		for range sigs {
			p.Send(signalMsg{})
		}
	}()

	// The scan runs in the background; each folder shows up when measured.
	// Send returns at once after the screen has closed.
	var finder sweep.Finder // one per scan, so each folder's files are read once
	m.progress.OnFound = func(c sweep.Candidate) {
		rel, _ := filepath.Rel(root, c.Path)
		p.Send(foundMsg{c, printable(rel), projectOf(&finder, root, c.Path)})
	}
	go func() {
		cands, kept := sweep.Scan(root, t, e.workers, m.progress)
		p.Send(scanDoneMsg{cands, len(kept)})
	}()
	_, err := p.Run()
	if e.flushInput != nil {
		e.flushInput() // a terminal reply that came in late, so it's not typed at the prompt
	}
	// Run can end with an error while deletions run; let them finish. Taking
	// every slot also stops queued ones from starting.
	for range cap(sem) {
		sem <- struct{}{}
	}
	return err
}

// projectOf is the project the folder at path belongs to, or nil.
func projectOf(f *sweep.Finder, root, path string) *project {
	pr, ok := f.Find(root, filepath.Dir(path))
	if !ok {
		return nil
	}
	name, _ := filepath.Rel(root, pr.Dir)
	if name == "." {
		name = filepath.Base(root)
	}
	return &project{pr, printable(name)}
}

// diskFree is the space left for files on dir's disk, or 0 if it can't
// tell; a full disk is 1 byte, which shows as 0.0 KB. On APFS, space held by local Time Machine snapshots comes back later.
func diskFree(dir string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0
	}
	return max(int64(st.Bavail)*int64(st.Bsize), 1)
}

// quitOnEOF calls quit when reading fails, EOF included. It waits first:
// Bubble Tea reads ahead and may still be passing on keys from the last read
// (holding a lone esc for its 50ms timeout), and those come before the quit.
type quitOnEOF struct {
	io.Reader
	quit func()
}

func (q quitOnEOF) Read(b []byte) (int, error) {
	n, err := q.Reader.Read(b)
	if err != nil {
		time.AfterFunc(200*time.Millisecond, q.quit) // ponytail: a delay, not a handshake; Bubble Tea has no hook for "input done"
	}
	return n, err
}

type quitOnEOFFile struct {
	*os.File
	quit func()
}

func (q quitOnEOFFile) Read(b []byte) (int, error) {
	return quitOnEOF{q.File, q.quit}.Read(b)
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
