//go:build darwin && arm64

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/devloop01/sweep/internal/sweep"
)

// Bubble Tea parses the terminal input; this checks its key names map to
// the screen's. (The interactive tests below type raw bytes end to end.)
func TestKeyNames(t *testing.T) {
	for k, want := range map[tea.KeyPressMsg]string{
		{Code: tea.KeyUp}: "up", {Code: 'k', Text: "k"}: "up",
		{Code: tea.KeyDown}: "down", {Code: 'j', Text: "j"}: "down",
		{Code: tea.KeyPgUp}: "pgup", {Code: tea.KeyPgDown}: "pgdn",
		{Code: tea.KeyHome}: "home", {Code: 'g', Text: "g"}: "home",
		{Code: tea.KeyEnd}: "end", {Code: 'g', Text: "G", Mod: tea.ModShift}: "end",
		{Code: tea.KeySpace, Text: " "}: "space", {Code: tea.KeyEnter}: "enter",
		{Code: 'a', Text: "a"}: "all", {Code: 'q', Text: "q"}: "quit",
		{Code: tea.KeyEscape}: "esc", {Code: 'c', Mod: tea.ModCtrl}: "quit", {Code: 'd', Mod: tea.ModCtrl}: "quit",
		{Code: tea.KeyLeft}: "left", {Code: tea.KeyRight}: "right", {Code: tea.KeyTab}: "next", {Code: 'c', Text: "c"}: "fold",
		{Code: 'y', Text: "y"}: "yes", {Code: 'n', Text: "n"}: "no",
		{Code: 'd', Text: "d"}: "delete", {Code: tea.KeyBackspace}: "delete", {Code: tea.KeyDelete}: "delete",
		{Code: 'x', Text: "x"}: "", // other keys do nothing
	} {
		if got := keyNames[k.String()]; got != want {
			t.Errorf("%q maps to %q, want %q", k.String(), got, want)
		}
	}
}

// confirmed presses d and answers y in the dialog.
func confirmed(s *screen) []sweep.Candidate {
	s.key("delete")
	return s.key("yes")
}

// d only asks. Cancel has focus, so a held-down d, or enter in the dialog,
// never deletes; the dialog deletes the folders it asked about even if the
// list re-sorts.
func TestScreenConfirm(t *testing.T) {
	s := newScreen(5)
	s.moved = true
	if s.key("delete") != nil || len(s.ask) != 1 {
		t.Fatalf("d should only open the dialog, ask %v", s.ask)
	}
	if s.key("enter") != nil || s.ask != nil || s.items[0].state != ready {
		t.Fatal("enter on Cancel should close the dialog and delete nothing")
	}
	for _, k := range []string{"no", "esc"} {
		s.key("delete")
		if s.key(k) != nil || s.ask != nil || s.quitting {
			t.Errorf("%s: should cancel without quitting", k)
		}
	}
	s.key("delete")
	if s.key("down") != nil || s.cursor != 0 || s.ask == nil {
		t.Error("other keys should leave the dialog open and the cursor alone")
	}
	s.key("right")                                                // Delete
	s.add(sweep.Candidate{Path: "/r/new", Size: 99 << 20}, "new") // a re-sort
	if del := s.key("enter"); len(del) != 1 || del[0].Path != "/r/p00/node_modules" {
		t.Fatalf("Delete deleted %v, want the folder it asked about", del)
	}
	s.key("down")
	s.key("delete")
	if s.key("quit"); !s.quitting || s.ask != nil {
		t.Error("quit should close the dialog and quit")
	}
}

func newScreen(n int) *screen {
	s := &screen{}
	for i := range n {
		path := fmt.Sprintf("p%02d/node_modules", i)
		s.add(sweep.Candidate{Path: "/r/" + path, Size: int64(n-i) << 20}, path)
	}
	return s
}

func TestScreenKeys(t *testing.T) {
	s := newScreen(5)
	for _, k := range []string{"up", "down", "down", "end", "down", "home", "pgdn"} {
		s.key(k)
	}
	if s.cursor != 1 { // with no view drawn yet, a page is one row
		t.Errorf("cursor %d, want 1", s.cursor)
	}
	s.key("home")
	// With nothing selected, enter deletes the highlighted folder.
	del := confirmed(s)
	if len(del) != 1 || del[0].Path != s.items[0].cand.Path || s.items[0].state != deleting {
		t.Fatalf("enter returned %v, state %v", del, s.items[0].state)
	}
	// The cursor stays put, so a held-down enter deletes only one folder.
	if again := confirmed(s); again != nil || s.cursor != 0 {
		t.Errorf("second enter returned %v, cursor %d", again, s.cursor)
	}
	s.done(del[0].Path, nil)
	s.key("space") // a deleted folder can't be selected
	if s.items[0].state != deleted || s.freed != 5<<20 || confirmed(s) != nil || s.items[0].selected {
		t.Errorf("after delete: %+v freed %d", s.items[0], s.freed)
	}

	// space selects (and unselects); enter deletes only the selection.
	s.key("down")
	s.key("space") // 1
	s.key("down")
	s.key("space") // 2
	s.key("space") // 2 again: unselected
	s.key("down")
	s.key("space") // 3
	del = confirmed(s)
	if len(del) != 2 || del[0].Path != s.items[1].cand.Path || del[1].Path != s.items[3].cand.Path {
		t.Fatalf("enter deleted %v, want items 1 and 3", del)
	}
	if s.items[1].selected || s.items[2].state != ready {
		t.Error("selection not cleared, or an unselected folder touched")
	}
	s.done(del[0].Path, fmt.Errorf("permission denied"))
	s.done(del[1].Path, nil)
	if s.items[1].state != failed || s.message != "permission denied" || s.items[3].state != deleted {
		t.Errorf("after enter: %+v %+v %q", s.items[1], s.items[3], s.message)
	}

	// a selects every folder still there, then unselects them all.
	s.key("all")
	if !s.items[2].selected || !s.items[4].selected || s.items[0].selected || s.items[1].selected {
		t.Errorf("all selected the wrong folders: %+v", s.items)
	}
	s.key("all")
	if slices.ContainsFunc(s.items, func(it item) bool { return it.selected }) {
		t.Error("second a didn't unselect all")
	}

	s.key("up") // folder 2, still there
	s.key("space")
	del = confirmed(s)
	s.key("quit")
	if s.finished() {
		t.Error("finished while a deletion is still running")
	}
	if s.key("space"); confirmed(s) != nil {
		t.Error("deleted after quit")
	}
	s.done(del[0].Path, nil)
	if !s.finished() {
		t.Error("not finished after quit and the last deletion")
	}
	if (&screen{}).key("delete") != nil || (&screen{}).key("end") != nil {
		t.Error("keys on an empty list")
	}
}

// Before any key the cursor stays on the top row as bigger folders arrive,
// but a space or enter right after the top changed does nothing.
func TestScreenCursorStartsAtTop(t *testing.T) {
	s := &screen{scanning: true}
	s.add(sweep.Candidate{Path: "/r/a", Size: 1}, "a")
	s.add(sweep.Candidate{Path: "/r/b", Size: 5}, "b")
	if s.cursor != 0 {
		t.Fatalf("cursor on row %d, want the top", s.cursor)
	}
	for _, k := range []string{"left", "right", "yes", "no"} { // dialog keys, no dialog open
		s.key(k)
	}
	if s.moved {
		t.Fatal("a dialog key unpinned the cursor")
	}
	if c := s.key("delete"); c != nil {
		t.Fatalf("d right after the top changed deleted %v", c)
	}
	s.add(sweep.Candidate{Path: "/r/c", Size: 9}, "c")
	if s.items[s.cursor].path != "b" {
		t.Errorf("after a key the cursor should follow b, on %q", s.items[s.cursor].path)
	}
}

// While the scan adds folders (re-sorting the list), the cursor stays on the
// same folder, so space deletes the folder that was highlighted, not
// whatever slid into its row. Finished deletions are matched by path.
func TestScreenCursorFollowsFolder(t *testing.T) {
	s := &screen{scanning: true}
	add := func(name string, size int64) { s.add(sweep.Candidate{Path: "/r/" + name, Size: size}, name) }
	add("a", 3)
	add("b", 1)
	s.moved = true
	s.key("down") // on b
	add("c", 5)   // sorts to the top: c, a, b
	add("d", 2)   // c, a, d, b
	if s.items[s.cursor].path != "b" {
		t.Fatalf("cursor on %q, want b", s.items[s.cursor].path)
	}
	s.key("space") // select b
	add("f", 6)    // everything moves down a row
	c := confirmed(s)
	if len(c) != 1 || c[0].Path != "/r/b" {
		t.Fatalf("enter deleted %v, want b", c)
	}
	add("e", 4) // b's row moves again before its deletion finishes
	s.done("/r/b", nil)
	if s.items[s.find("/r/b")].state != deleted || s.items[s.find("/r/e")].state != ready {
		t.Error("deletion recorded on the wrong row")
	}
	// The scan's final sizes re-sort too; the cursor still follows b, and a
	// deleted folder keeps the size it was deleted with.
	s.finish([]sweep.Candidate{{Path: "/r/a", Size: 9}, {Path: "/r/b", Size: 8}, {Path: "/r/c", Size: 5}, {Path: "/r/d", Size: 2}, {Path: "/r/e", Size: 4}, {Path: "/r/f", Size: 6}}, 2)
	if s.items[s.cursor].path != "b" || s.items[0].path != "a" || s.scanning || s.skipped != 2 {
		t.Errorf("after finish: cursor on %q, first %q, scanning %v", s.items[s.cursor].path, s.items[0].path, s.scanning)
	}
	if s.items[s.find("/r/b")].cand.Size != 1 || s.total != 9+1+5+2+4+6 {
		t.Errorf("sizes after finish: b %d, total %d", s.items[s.find("/r/b")].cand.Size, s.total)
	}
}

var escapes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestScreenView(t *testing.T) {
	s := newScreen(40)
	s.items[3].path = strings.Repeat("very-long-folder/", 10) + "node_modules"
	for _, size := range [][2]int{{80, 24}, {40, 10}, {20, 6}} {
		w, h := size[0], size[1]
		s.cursor = 30
		lines := s.view(look{dark: true, spin: "⠋"}, w, h)
		if len(lines) != h {
			t.Errorf("%dx%d: %d lines", w, h, len(lines))
		}
		for _, l := range lines {
			if n := utf8.RuneCountInString(escapes.ReplaceAllString(l, "")); n > w {
				t.Errorf("%dx%d: line %d wide: %q", w, h, n, l)
			}
		}
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(escapes.ReplaceAllString(l, ""), " ▌") }) {
			t.Errorf("%dx%d: cursor row scrolled out of view", w, h)
		}
	}
	s.free = 38 << 30
	if head := escapes.ReplaceAllString(s.view(look{}, 80, 24)[1], ""); !strings.Contains(head, "· 38.0 GB free") {
		t.Errorf("header: %q", head)
	}
	if head := escapes.ReplaceAllString(s.view(look{}, 40, 24)[1], ""); strings.Contains(head, "free") {
		t.Errorf("free space cut to fit 40 columns: %q", head)
	}
	s.free = 0
	if help := escapes.ReplaceAllString(s.view(look{}, 80, 24)[23], ""); !strings.Contains(help, "space select") || !strings.Contains(help, "q quit") {
		t.Errorf("help bar: %q", help)
	}
	s.cursor = 3
	lines := s.view(look{}, 60, 24)
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "…") && strings.Contains(l, "/node_modules") }) {
		t.Error("long path not cut from the left")
	}
	// The dialog for that long path fits a narrow terminal, and a short one
	// doesn't grow the frame.
	s.ask = []string{s.items[3].cand.Path}
	box := escapes.ReplaceAllString(strings.Join(s.view(look{}, 44, 24), "\n"), "")
	if !strings.Contains(box, "Cancel ") || !strings.Contains(box, ")?") {
		t.Errorf("dialog cut off at 44 columns:\n%s", box)
	}
	if n := len(s.view(look{}, 44, 4)); n != 4 {
		t.Errorf("dialog made a 4-line terminal %d lines tall", n)
	}
}

// A project's folders sit under a header row. The header is never a folder:
// it isn't counted, selected by a, or deleted; space and enter on it act on
// its folders.
func TestScreenProjects(t *testing.T) {
	s := &screen{tree: true}
	// A new *project per folder, as the scan sends them.
	mono := func() *project { return &project{sweep.Project{Dir: "/r/mono", Kind: "Turborepo"}, "mono"} }
	s.addIn(sweep.Candidate{Path: "/r/mono/node_modules", Size: 5}, "mono/node_modules", mono())
	s.add(sweep.Candidate{Path: "/r/big/node_modules", Size: 8}, "big/node_modules")
	s.addIn(sweep.Candidate{Path: "/r/mono/apps/web/.next", Size: 4}, "mono/apps/web/.next", mono())
	// mono's 9 bytes beat big's 8: header, its two folders, then big.
	if len(s.rows) != 4 || !s.rows[0].head || s.rows[0].n != 2 || !s.rows[2].last || s.items[s.rows[3].i].path != "big/node_modules" {
		t.Fatalf("rows %+v, items %+v", s.rows, s.items)
	}
	if s.items[s.rows[2].i].sub != "apps/web/.next" {
		t.Errorf("sub path %q", s.items[s.rows[2].i].sub)
	}
	if head := escapes.ReplaceAllString(s.view(look{}, 80, 24)[1], ""); !strings.Contains(head, "3 folders") {
		t.Errorf("header counts project rows: %q", head)
	}
	s.key("down") // cursor follows the header across a re-sort
	s.key("up")
	s.add(sweep.Candidate{Path: "/r/huge/node_modules", Size: 20}, "huge/node_modules")
	if !s.rows[s.cursor].head {
		t.Fatalf("cursor left the project: on row %+v", s.rows[s.cursor])
	}
	s.key("space")
	if !s.items[s.rows[s.cursor+1].i].selected || !s.items[s.rows[s.cursor+2].i].selected {
		t.Error("space on a project should select its folders")
	}
	s.key("space")
	if slices.ContainsFunc(s.items, func(it item) bool { return it.selected }) {
		t.Error("space again should unselect them")
	}
	s.key("delete")
	if box := escapes.ReplaceAllString(strings.Join(s.view(look{}, 80, 24), "\n"), ""); !strings.Contains(box, "Delete 2 folders in mono (0.0 KB)?") {
		t.Errorf("dialog doesn't name the project:\n%s", box)
	}
	slices.Sort(s.ask)
	if !slices.Equal(s.ask, []string{"/r/mono/apps/web/.next", "/r/mono/node_modules"}) {
		t.Errorf("enter on a project asked about %v", s.ask)
	}
	del := s.key("yes")
	if len(del) != 2 || s.total != 37 {
		t.Errorf("deleted %v, total %d", del, s.total)
	}
	s.key("all")
	if n := len(slices.DeleteFunc(slices.Clone(s.items), func(it item) bool { return !it.selected })); n != 2 {
		t.Errorf("a selected %d folders, want the 2 left", n)
	}
	// t turns the project rows off; the cursor moves to the project's first folder.
	s.key("tree")
	if len(s.rows) != len(s.items) || s.items[s.rows[s.cursor].i].path != "mono/node_modules" {
		t.Errorf("after t: %d rows, cursor on %q", len(s.rows), s.items[s.rows[s.cursor].i].path)
	}
}

// In the project view ← collapses the cursor's project (from a folder in it,
// the cursor goes up to its row), → opens it, tab and shift+tab hop between
// projects, and c collapses or opens them all.
func TestScreenFold(t *testing.T) {
	s := &screen{tree: true}
	proj := func(dir string) *project { return &project{sweep.Project{Dir: "/r/" + dir, Kind: "Node"}, dir} }
	for _, f := range []struct {
		dir, sub string
		size     int64
	}{{"a", "node_modules", 9}, {"a", ".next", 8}, {"b", "node_modules", 5}, {"b", ".turbo", 4}} {
		s.addIn(sweep.Candidate{Path: "/r/" + f.dir + "/" + f.sub, Size: f.size}, f.dir+"/"+f.sub, proj(f.dir))
	}
	on := func() string {
		r := s.rows[s.cursor]
		if r.head {
			return s.items[r.i].proj.name
		}
		return s.items[r.i].path
	}
	if s.key("next"); on() != "b" {
		t.Fatalf("tab went to %q, want b", on())
	}
	// enter on a project's row collapses and opens it, never asks.
	if s.key("enter"); len(s.rows) != 4 || on() != "b" || s.ask != nil {
		t.Fatalf("enter on a project: %d rows, on %q, ask %v", len(s.rows), on(), s.ask)
	}
	if s.key("enter"); len(s.rows) != 6 {
		t.Fatalf("enter again should open it: %d rows", len(s.rows))
	}
	s.key("down") // b/node_modules: enter selects it, like space
	if s.key("enter"); !s.items[s.rows[s.cursor].i].selected || s.ask != nil {
		t.Fatal("enter on a folder should select it")
	}
	s.key("enter")
	if s.key("left"); on() != "b" || len(s.rows) != 4 {
		t.Fatalf("← from a folder: on %q, %d rows", on(), len(s.rows))
	}
	if s.key("prev"); on() != "a" {
		t.Fatalf("shift+tab went to %q, want a", on())
	}
	if s.key("right"); len(s.rows) != 4 || on() != "a" {
		t.Errorf("→ on an open project changed something: %d rows on %q", len(s.rows), on())
	}
	s.key("fold")
	if len(s.rows) != 2 || on() != "a" {
		t.Fatalf("c: %d rows, on %q", len(s.rows), on())
	}
	s.key("space") // still selects a collapsed project's folders
	if !s.items[0].selected || !s.items[1].selected {
		t.Error("space on a collapsed project should select its folders")
	}
	if s.key("fold"); len(s.rows) != 6 {
		t.Errorf("c again should open all: %d rows", len(s.rows))
	}
	s.key("delete")
	if s.key("next"); !s.yes {
		t.Error("tab in the dialog should move to Delete")
	}
}

// s cycles the order: size, name, most recent change, and back. Projects
// sort by their total or newest folder; the cursor stays on its folder.
func TestScreenSort(t *testing.T) {
	now := time.Now()
	s := &screen{}
	for _, f := range []struct {
		path string
		size int64
		ago  time.Duration
	}{{"b/node_modules", 9, 48 * time.Hour}, {"c/node_modules", 5, time.Hour}, {"a/node_modules", 1, 24 * time.Hour}} {
		s.add(sweep.Candidate{Path: "/r/" + f.path, Size: f.size, Touched: now.Add(-f.ago)}, f.path)
	}
	order := func() (got []string) {
		for _, r := range s.rows {
			if r.head {
				got = append(got, s.items[r.i].proj.name+"/")
			} else {
				got = append(got, s.items[r.i].path[:1])
			}
		}
		return got
	}
	s.key("down") // on c
	for _, want := range []string{"abc", "cab", "bca"} {
		s.key("sort")
		if got := strings.Join(order(), ""); got != want {
			t.Errorf("order %d: %s, want %s", s.order, got, want)
		}
		if s.items[s.rows[s.cursor].i].path != "c/node_modules" {
			t.Errorf("order %d: cursor left c", s.order)
		}
	}
	// a's project holds the oldest and the newest folder: newest wins.
	s.tree = true
	s.addIn(sweep.Candidate{Path: "/r/a/.next", Size: 1, Touched: now}, "a/.next", &project{sweep.Project{Dir: "/r/a"}, "a"})
	s.items[s.find("/r/a/node_modules")].proj = &project{sweep.Project{Dir: "/r/a"}, "a"}
	s.key("sort")
	s.key("sort")
	if got := strings.Join(order(), ""); got != "a/aacb" {
		t.Errorf("projects by recent: %s", got)
	}
}

// Clicks: a row moves the cursor, its checkbox selects it, ▾ folds a
// project, a column name sorts by it, and the dialog's buttons work. A click
// hits the folder drawn there, even if the list re-sorted since.
func TestScreenClick(t *testing.T) {
	s := newScreen(5)
	s.view(look{}, 80, 24)
	d := s.drawn
	if s.click(30, d.list+2); s.cursor != 2 || s.items[2].selected {
		t.Fatalf("click on a row: cursor %d", s.cursor)
	}
	if s.click(4, d.list+3); s.cursor != 3 || !s.items[3].selected {
		t.Fatal("click on a checkbox should select it")
	}
	fresh := func() { s.lastClick.row = "" }
	s.click(30, d.list+1)
	if s.click(30, d.list+1); !s.items[1].selected {
		t.Fatal("double-click on a row should select it")
	}
	if s.click(30, d.list+1); !s.items[1].selected {
		t.Fatal("a third click shouldn't unselect it")
	}
	fresh()
	s.click(30, d.list+1)
	if s.click(30, d.list+1); s.items[1].selected {
		t.Fatal("another double-click should unselect it")
	}
	fresh()
	s.click(30, d.list+1)
	s.lastClick.at = time.Now().Add(-time.Second)
	if s.click(30, d.list+1); s.items[1].selected {
		t.Fatal("two slow clicks aren't a double-click")
	}
	fresh()
	s.click(30, d.list+1)
	if s.click(30, d.list+2); s.items[2].selected || s.items[1].selected {
		t.Fatal("clicks on two rows aren't a double-click")
	}
	s.add(sweep.Candidate{Path: "/r/new", Size: 99 << 20}, "new") // re-sorts, not drawn yet
	if s.click(30, d.list); s.items[s.rows[s.cursor].i].path != "p00/node_modules" {
		t.Errorf("click hit %q, want the folder drawn there", s.items[s.rows[s.cursor].i].path)
	}
	s.view(look{}, 80, 24)
	if s.click(d.cols[0][0]+1, s.drawn.head); s.order != byName {
		t.Errorf("click on Folder: order %d", s.order)
	}
	if s.click(s.drawn.cols[1][0]+1, s.drawn.head); s.order != byRecent {
		t.Errorf("click on Touched: order %d", s.order)
	}

	s.key("delete")
	s.view(look{}, 80, 24)
	if s.drawn.buttons < 0 {
		t.Fatal("dialog buttons not found")
	}
	if s.click(s.drawn.can[0]+1, s.drawn.buttons); s.ask != nil {
		t.Error("Cancel didn't close the dialog")
	}
	s.key("delete")
	s.view(look{}, 80, 24)
	if del := s.click(s.drawn.del[0]+1, s.drawn.buttons); len(del) != 1 {
		t.Errorf("Delete deleted %v", del)
	}

	// ▾ on a project's row folds it.
	p := &screen{tree: true}
	proj := &project{sweep.Project{Dir: "/r/a"}, "a"}
	p.addIn(sweep.Candidate{Path: "/r/a/node_modules", Size: 2}, "a/node_modules", proj)
	p.addIn(sweep.Candidate{Path: "/r/a/.next", Size: 1}, "a/.next", proj)
	p.view(look{}, 80, 24)
	if p.click(8, p.drawn.list); len(p.rows) != 1 {
		t.Errorf("click on ▾: %d rows", len(p.rows))
	}
	// The key bar fits 80 columns, with c in the project view and a selection.
	p.items[0].selected, p.items[1].selected = true, true
	for _, tree := range []bool{true, false} {
		p.tree = tree
		bar := escapes.ReplaceAllString(p.view(look{}, 80, 24)[23], "")
		if !strings.Contains(bar, "q quit") || strings.Contains(bar, "c fold all") != tree || strings.Contains(bar, "a all") == tree {
			t.Errorf("key bar (projects %v): %q", tree, bar)
		}
	}
}

// ? opens the key list; the next key only closes it. o asks to show the
// highlighted folder in Finder.
func TestScreenHelpAndReveal(t *testing.T) {
	s := newScreen(3)
	s.key("help")
	if !s.helping || !strings.Contains(escapes.ReplaceAllString(strings.Join(s.view(look{}, 80, 24), "\n"), ""), "Press any key to close") {
		t.Fatal("? didn't open the key list")
	}
	if s.key("enter"); s.helping || s.ask != nil {
		t.Errorf("enter should only close the key list, ask %v", s.ask)
	}
	s.key("help")
	if s.key("quit"); s.helping || s.quitting {
		t.Error("q should only close the key list")
	}
	s.key("down")
	s.key("open")
	if s.reveal != "/r/p01/node_modules" {
		t.Errorf("o reveals %q", s.reveal)
	}
	s.key("delete")
	if s.key("help"); s.helping {
		t.Error("? opened the key list over the confirm dialog")
	}
	// A signal or end of input quits even with the key list open.
	s.ask = nil
	s.key("help")
	if s.quit(); !s.quitting || s.helping {
		t.Error("quit() with the key list open didn't quit")
	}
}

// With one folder listed, a bigger one arriving mustn't slide under the
// cursor once a key has been pressed.
func TestScreenCursorFollowsOnlyFolder(t *testing.T) {
	s := &screen{scanning: true}
	s.add(sweep.Candidate{Path: "/r/a", Size: 1}, "a")
	s.key("down")
	s.add(sweep.Candidate{Path: "/r/b", Size: 9}, "b")
	if got := s.items[s.rows[s.cursor].i].path; got != "a" {
		t.Errorf("cursor on %q, want a", got)
	}
}

// diskFree reads a real disk: more than nothing, less than 1 PB.
func TestDiskFree(t *testing.T) {
	if f := diskFree(t.TempDir()); f <= 1 || f > 1<<50 {
		t.Errorf("diskFree = %d", f)
	}
	if f := diskFree("/no/such/dir"); f != 0 {
		t.Errorf("diskFree of a missing folder = %d, want 0", f)
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{{time.Hour, "today"}, {3 * 24 * time.Hour, "3d ago"}, {70 * 24 * time.Hour, "2mo ago"}, {800 * 24 * time.Hour, "2y ago"}} {
		if got := age(now.Add(-c.ago), now); got != c.want {
			t.Errorf("age(-%v) = %q, want %q", c.ago, got, c.want)
		}
	}
	if age(time.Time{}, now) != "" {
		t.Error("zero time should be blank")
	}
}

// keysThen runs the interactive screen on the fixture and types input once
// the scan has finished.
func keysThen(t *testing.T, input string) (root string, code int, out string) {
	t.Helper()
	root = fixture(t)
	code, out = keysIn(t, root, input)
	return root, code, out
}

func keysIn(t *testing.T, root, input string, state ...string) (code int, out string) {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var o, e bytes.Buffer
	code = run([]string{root}, env{
		isTTY: true, cols: 80, size: func() (int, int) { return 80, 24 },
		afterScan: func() {
			go func() {
				pw.Write([]byte(input))
				pw.Close()
			}()
		},
		homes: []string{t.TempDir()}, stdin: pr, stdout: &o, stderr: &e, workers: 4,
		statePath: strings.Join(state, ""),
	})
	return code, o.String()
}

// The project view and sort order are remembered for the next run; a run
// that changes neither writes nothing.
func TestInteractiveRemembersView(t *testing.T) {
	state := filepath.Join(t.TempDir(), "sweep", "screen.json")
	keysIn(t, fixture(t), "q", state)
	if _, err := os.Stat(state); err == nil {
		t.Fatal("a run that changed nothing wrote the state file")
	}
	keysIn(t, fixture(t), "tsq", state) // project view, sort by name
	if b, _ := os.ReadFile(state); string(b) != `{"projects":true,"sort":"name"}`+"\n" {
		t.Fatalf("state file: %q", b)
	}
	_, out := keysIn(t, fixture(t), "q", state)
	if !strings.Contains(escapes.ReplaceAllString(out, ""), "Folder↓") {
		t.Error("next run didn't open sorted by name")
	}
	os.WriteFile(state, []byte("not json"), 0o644)
	if _, out = keysIn(t, fixture(t), "q", state); !strings.Contains(escapes.ReplaceAllString(out, ""), "Size↓") {
		t.Error("a broken state file should mean the defaults")
	}
}

// A Turborepo's folders are listed under its name; enter there deletes
// them and nothing else.
func TestInteractiveDeletesProject(t *testing.T) {
	root := fixture(t)
	for path, size := range map[string]int{
		"mono/turbo.json": 0, "mono/package.json": 2, "mono/node_modules/f": 50_000,
		"mono/apps/web/package.json": 2, "mono/apps/web/.next/f": 1,
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755)
		os.WriteFile(filepath.Join(root, path), make([]byte, size), 0o644)
	}
	code, out := keysIn(t, root, "tgdyq") // t: list by project
	plain := escapes.ReplaceAllString(out, "")
	if !strings.Contains(plain, "mono  Turborepo") || !regexp.MustCompile(`└─ +(\[ \] )?apps/web/\.next`).MatchString(plain) {
		t.Errorf("no project row:\n%s", plain)
	}
	for _, gone := range []string{"mono/node_modules", "mono/apps/web/.next"} {
		if _, err := os.Lstat(filepath.Join(root, gone)); err == nil {
			t.Errorf("%s not deleted", gone)
		}
	}
	for _, kept := range []string{"mono/turbo.json", "mono/apps/web/package.json"} {
		if _, err := os.Lstat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s deleted", kept)
		}
	}
	if code != 0 || targetsLeft(root) != 3 || !strings.Contains(out, "Deleted 2 folders") {
		t.Errorf("code %d, left %d, out:\n%q", code, targetsLeft(root), out)
	}
}

// With nothing to clean the screen closes by itself once the scan ends.
func TestInteractiveNothingToClean(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	var o, e bytes.Buffer
	code := run([]string{root}, env{
		isTTY: true, cols: 80, size: func() (int, int) { return 80, 24 },
		homes: []string{t.TempDir()}, stdin: pr, stdout: &o, stderr: &e, workers: 4,
	})
	if code != 0 || !strings.HasSuffix(o.String(), "Nothing to clean.\n") {
		t.Errorf("code %d, out:\n%q", code, o.String())
	}
}

// The list is a/node_modules, c/.svelte-kit, e/weird?name/node_modules
// (biggest first, then by path). g goes to the top: the cursor otherwise
// stays on whichever folder the scan found first.
func TestInteractiveDeletesHighlighted(t *testing.T) {
	root, code, out := keysThen(t, "gjdyq")
	if code != 0 || !strings.Contains(out, "Deleted 1 folder, freed about") {
		t.Errorf("code %d, out:\n%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "c/.svelte-kit")); err == nil {
		t.Error("highlighted folder not deleted")
	}
	if targetsLeft(root) != 2 {
		t.Errorf("%d targets left, want 2", targetsLeft(root))
	}
	before, _, _ := strings.Cut(out, "Deleted")
	if !strings.Contains(before, "\x1b[?1049h") || !strings.Contains(before, "\x1b[?1049l") || !strings.Contains(before, "\x1b[?25h") {
		t.Errorf("alternate screen not entered and left before the summary: %q", out[max(len(out)-300, 0):])
	}
}

// Holding enter only selects and unselects. Holding d opens the dialog with
// Cancel focused and does nothing more, so neither deletes; ← moves to
// Delete.
func TestInteractiveHeldKeysDeleteNothing(t *testing.T) {
	for _, held := range []string{"g\r\r\r\r\rq", "gdddddq", "gd\r\r\r\rq"} {
		root, _, out := keysThen(t, held)
		if targetsLeft(root) != 3 || !strings.Contains(out, "Nothing deleted.") {
			t.Errorf("%q: left %d, out:\n%q", held, targetsLeft(root), out)
		}
	}
	root, _, out := keysThen(t, "gd\x1b[D\rq")
	if targetsLeft(root) != 2 || !strings.Contains(out, "Deleted 1 folder,") {
		t.Errorf("left %d, out:\n%q", targetsLeft(root), out)
	}
}

func TestInteractiveDeletesSelection(t *testing.T) {
	root, _, out := keysThen(t, "g jj dyq") // select a and e/…, delete both
	if _, err := os.Stat(filepath.Join(root, "c/.svelte-kit")); err != nil || targetsLeft(root) != 1 || !strings.Contains(out, "Deleted 2 folders,") {
		t.Errorf("left %d, out:\n%q", targetsLeft(root), out)
	}
	root, _, out = keysThen(t, "a\x7fyq") // everything, with ⌫
	if targetsLeft(root) != 0 || !strings.Contains(out, "Deleted 3 folders,") {
		t.Errorf("select all: left %d, out:\n%q", targetsLeft(root), out)
	}
}

func TestInteractiveQuitDeletesNothing(t *testing.T) {
	for _, input := range []string{"", "q", "\x03", "jjk\x1b", "g  a a q", "gdnq", "adn\x1b", "g\r\r\rq"} { // selecting alone deletes nothing
		root, code, out := keysThen(t, input)
		if code != 0 || targetsLeft(root) != 3 || !strings.Contains(out, "Nothing deleted.") {
			t.Errorf("%q: code %d, left %d, out:\n%q", input, code, targetsLeft(root), out)
		}
	}
}
