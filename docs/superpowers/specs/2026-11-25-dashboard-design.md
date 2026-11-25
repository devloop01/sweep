# Dashboard screen

## Why

The interactive screen works but reads as a plain list. The user wants it to
feel like a dashboard, in the spirit of btop: framed panels, bars instead of
bare numbers, the key facts visible at a glance. Second goal, in their words:
"not so hard to cleanup when necessary". Read as: freeing space should stay a
few keystrokes (call: mine). The design also keeps the change easy to back
out, which covers the other reading.

## Goals

- Panels with titles set in their top border, like btop.
- At a glance: disk used/total, what sweep could free, what's selected, and
  where the space is by kind of folder.
- A size bar on every list row.
- Numbers and bars fill in live while the scan runs.

## Non-goals

- Package-manager global caches. That's the next project, designed on its
  own (it touches the home/~/Library guard, runs each tool's own clean
  command instead of the Trash, and pnpm's hard-linked store skews sizes).
- New keys, options or settings. `a` then `d` already cleans everything in
  two keys.
- Graphs over time: nothing changes after the scan, so there's no history to plot.

## Anti-goals

- Any change to what gets deleted or how: `screen.key`, `answer`, `done`, the
  confirm dialog and the remove path stay as they are.
- Losing list rows on a small terminal to decoration.

## Layout

```
┌─ disk ───────────────────────────┐┌─ by kind ────────────────────────────────┐
│ ⣶⣶⣶⣶⣶⣶⣶⣶⣶⣀⣀⣀ 378.7 GB / 494.4 GB ││ dependencies ⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶  84.0 MB │
│                                  ││ build output ⣶⣶⣶⣶⣶⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀  24.0 MB │
│ free      115.6 GB               ││ framework    ⣶⣶⣆⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀  11.5 MB │
│ can free  125.6 MB               ││ python       ⣶⣆⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀   6.1 MB │
│ selected  58.3 MB · 4 folders    ││                                          │
│                                  ││                                          │
└──────────────────────────────────┘└──────────────────────────────────────────┘
┌─ folders ─ ~/Code/demo/projects · by size ───────────────────── 11 folders ─┐
│       Folder                                Touched       Size↓              │
│▌ [x]  agency/node_modules                     today     46.0 MB  ⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶ │
│  [ ]  engine/target                           today     24.0 MB  ⣶⣶⣶⣶⣶⣀⣀⣀⣀⣀ │
└──────────────────────────────────────────────────────────────────────────────┘
 space select • a all • d trash • t projects • ? help • q quit
```

- **disk:** gauge of used/total for DIR's disk, a gap line, then free,
  "can free" (sum of folders not yet moved), "selected" and "in Trash".
  While scanning, a spinner and the checked count follow "can free".
- **by kind:** one row per kind present, biggest first, bar scaled to the
  biggest kind. Deleted folders drop out of the sums.
- **folders:** the list, unchanged in behavior (tree view, folding, sort
  arrow, cursor, states), inside a frame whose title carries DIR (cut from
  the left so the count stays), the sort order and the folder count. Each
  row gets a size bar scaled to the biggest row, shown only when the path
  keeps 30 columns. Checkboxes are `[x]`, `[-]` (part of a project), `[ ]`.
- **Each fact once:** selected and moved sizes live in the disk panel only;
  the frame's bottom border carries other messages (errors, "Finishing
  moves…"); the key bar is just keys. Without panels, the status line under
  the list says what's selected or moved, as before.
- The frame runs edge to edge; the key bar on the last line stops one
  column short so a terminal can't scroll.
- Bars are braille, like btop's graphs: ⣶ solid in front, a ⣀ dotted track
  behind, half-cell ends (⣆). The bar skips each cell's top dot row, so
  bars on neighbouring lines have a gap.
- Confirm dialog and key list still overlay the whole screen.

## How (decided)

- View-only change in `tui.go`. Every panel number is computed from
  `s.items` at render time; no new state on `screen`.
- Kind of folder: a fixed name → kind map in `tui.go`, matching the
  README's groups (dependencies, framework caches, python caches, build
  output). Names from a user's `targets.conf` that aren't in it show as
  "other".
- Disk total: a `diskSize` beside `diskFree` (`Statfs`, `Blocks*Bsize`),
  read once; `screen` gets `disk` beside `free`.
- Border titles: lipgloss v2.0.6 has no border-title API, so the top edge
  of each panel is drawn by hand (`┌ title ───┐`); the sides and bottom can
  use lipgloss borders or be drawn the same way, builder's choice.
- Clicks: `s.drawn` (list line, column ranges, buttons) is shifted by the
  panels' height and the frame's left border so `click()` keeps working.
  Tests address rows through `s.drawn`, so most survive. Ones that check
  exact text may need updating.

## Small terminals (my call)

- Under about 70 columns or 20 rows, the top two panels collapse into the
  current one-line header (count · size · free), and the list frame keeps
  the rest.
- Under 12 rows, as today: no column names.
- Row size bars appear only when the path column still gets 30 or more
  columns.

## Left to the builder

- Exact colors: keep the existing accent and the red/yellow `sizeLook`
  scale for bars.
- Panel width split between disk and by kind (roughly 45/55).
- Whether the frame is drawn with lipgloss borders or by hand.

## Approaches considered

1. **Framed dashboard over the existing list (picked).** All the
   dashboard feel, smallest risk, and the delete path is untouched.
2. **Rewrite on bubbles components (table, viewport, progress).** Less
   hand-drawn layout, but it would redo the click mapping, tree view and
   tests the current list already gets right. Too much churn for a look
   change.
3. **Separate dashboard tab next to the list.** Two screens to keep in sync,
   and the at-a-glance numbers would hide while you pick folders.

## Next

Package-manager caches, designed as their own project, slotting in as one
more kind and one more group in this layout.
