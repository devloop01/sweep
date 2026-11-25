# Package caches

## Why

Package managers keep a shared download cache in the home folder, often
more space than every project's node_modules together. sweep couldn't see
them: it only scans the folder it's pointed at and refuses the home folder
and ~/Library. The user wanted them cleared from the dashboard.

## What

- A "package caches" group in the list (tree view), and a "pkg caches" kind
  in the by-kind panel. Flat view mixes them in by size.
- Cleared by the tool's own command, never moved to the Trash and never
  deleted by sweep: npm, bun, Go build cache, Go modules, Homebrew, pip, uv.
- The dialog says caches don't go to the Trash; rows say "clearing" and
  "✓ cleared"; the disk panel adds "cleared"; the exit summary adds
  "Cleared N caches"; the key bar says `d clear` when d would only clear.

## Decisions

- **Found from default folders plus env overrides, not by asking tools.**
  Asking took up to 5 s (pnpm behind a version-manager shim), yarn failed,
  corepack can download, and /usr/bin/pip3 opens Apple's install dialog.
  Tools in /usr/bin are skipped. Tools run only to clear, with no stdin,
  `COREPACK_ENABLE_DOWNLOAD_PROMPT=0` and `HOMEBREW_NO_AUTO_UPDATE=1`.
- **Size = files with no other hard link** (`measure(...).own`); freed =
  size before minus after, so a partial clean isn't overcounted.
- **pnpm left out:** on APFS pnpm clones into projects, so used store files
  look unshared and its size overstated what `pnpm store prune` frees by
  ~13 GB on the dev machine. yarn left out (shims, berry vs classic).
- **Never by habit:** `a` skips caches; `-n` and `-y` don't include them.
  Picked one by one, or via the group row.
- **Separate paths:** `item.cache` marks a cache; the model routes caches to
  `clean` and folders to `remove`, tested both ways. Clean runs through the
  same semaphore, so quitting waits for it. Tests inject both; the real
  runner is a package variable swapped in tests.
- The guard (home, ~/Library, dot folders) is unchanged: it governs the
  scan, and caches don't go through the scan.
- "Nothing to clean" waits for both the scan and the cache search.
- Caches are named after their tool ("Homebrew", "Go build"); flat view
  adds "cache · ~/path" so they don't read as folders. Clearing one shows
  the exact command in the dialog, since `brew cleanup` also removes old
  versions of installed formulae.
