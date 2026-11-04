# sweep

Free up disk space by deleting `node_modules`, build caches (`.next`,
`.svelte-kit`, `.turbo`, …), Rust `target` and Python caches from all your
projects at once.

![sweep listing build folders, grouping them by project, sorting by last change, and deleting two after confirming](demo/sweep.gif)

Everything it deletes comes back with `npm install` / `pnpm install`, or
your next dev, build, cargo or test run.

## Install

For Apple Silicon Macs.

```sh
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh
```

Or, if you have Go: `go install github.com/devloop01/sweep@latest`

If your terminal then says `sweep: command not found`, add the install folder
to your PATH and open a new terminal:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zprofile   # use ~/go/bin if you installed with Go
```

Please don't download the binary with your browser: macOS will block it,
because it isn't signed by Apple.

The installer checks the download for you and prints its checksum. To check
a copy it installed later on, run `shasum -a 256 "$(which sweep)"` and compare it
with the `sha256` shown next to `sweep-darwin-arm64` on the
[release page](https://github.com/devloop01/sweep/releases/latest).

## Use

Go to the folder that holds your projects and run `sweep`:

```sh
cd ~/Documents/Code
sweep
```

The list opens right away and fills in while sweep scans, biggest first.
Move with ↑/↓ (or j/k), select folders with **space** or **enter** (`a`
selects all), and press **d** (or ⌫) to delete them. With nothing selected,
d deletes the highlighted folder. sweep asks first: `y` deletes, `n` or esc
cancels.

Press `t` to list folders under their project, a whole monorepo (Turborepo,
Nx, pnpm or npm workspaces) as one project. Enter on a project's name
collapses or opens it, space selects all its folders, and d deletes them.
←/→ collapse and open too, tab jumps to the next project, and `c` collapses
or opens them all.

`s` sorts by size, name, or last change. sweep remembers the project view
and sort order for next time. `o` shows a folder in Finder, `?` lists every
key, and `q` quits.

The mouse works too: scroll, click a row or its checkbox (or double-click
the row) to select it, click ▾ to fold a project, a column name to sort by
it, or the dialog's buttons.

To just look, or to clean everything at once:

```sh
sweep -n     # only print the list, delete nothing
sweep -y     # delete everything in the list, no questions
```

In a long printed list, folders under 1 MB are folded into one line. You can
also pass a folder instead of using `cd`: `sweep -n ~/Documents/Code`.

## What gets deleted

Only folders that are safe to throw away, because your tools rebuild them
for free:

- **Dependencies:** `node_modules`
- **Framework caches:** `.next` `.svelte-kit` `.turbo` `.nuxt` `.output`
  `.astro` `.vite` `.parcel-cache` `.angular` `.docusaurus` `.nx` `.vinxi`
  `storybook-static` and `.vercel/output`
- **Python caches:** `.pytest_cache` `.mypy_cache` `.ruff_cache`
- **Build output:** `dist` `build` `out`, and `target` (Rust, Maven), but
  only in a git repo whose `.gitignore` lists them, with nothing inside
  tracked. Projects sometimes keep real files in these (a committed `dist`,
  scripts in `build`, new files not committed yet), so outside git, or when
  git tracks or doesn't ignore them, sweep skips them and says how many it
  skipped.

sweep never deletes anything that would make you set up a project again, like
`.vercel` or `.netlify` (your project link) or `.wrangler` (local Cloudflare
data). You can add those yourself if you want.

## Customize

Create `~/.config/sweep/targets.conf`, one folder name per line, like a
`.gitignore`:

```
# also delete these
.cache
coverage

# never delete these
!.output
!out
```

Names you list are deleted too. Put `!` in front of a name to stop deleting
it. `parent/child` deletes only `child` inside `parent`, like `.vercel/output`.
If the file has a mistake, sweep tells you the line and doesn't run.

## Skip a folder

To keep sweep out of a folder, put an empty `.sweepignore` file in it:

```sh
touch ~/Documents/Code/my-project/.sweepignore
```

sweep won't look inside it.

## Safety

sweep only deletes folders with the names above, and shows you the list
first unless you pass `-y`. It also:

- won't run in your home folder, `~/Library`, system folders, or with `sudo`
- never follows shortcuts (symlinks), and never touches `.git` folders or apps
- skips any folder that git tracks files in
- stays on your Mac's drive: it won't go into network shares or USB drives
  mounted inside the folder

Sizes are real disk usage. With pnpm, the freed space can be smaller than
shown, because pnpm keeps a shared copy of every package.

## Upgrade or uninstall

- **Upgrade:** `sweep upgrade`, or run the install command again. sweep tells
  you when a new release is out.
- **Uninstall:** `rm "$(which sweep)"`, and `rm -r ~/.local/state/sweep`
  where it remembers the screen's view.

---

Working on sweep itself? See [DEVELOPMENT.md](DEVELOPMENT.md).
[MIT License](LICENSE).
