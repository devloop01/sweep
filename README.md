# sweep

Clean up `node_modules` and build caches across all your projects, and get
your disk space back.

![sweep listing build folders, grouping them by project, sorting by last change, and moving two to the Trash after confirming](demo/sweep.gif)

## Install

On an Apple Silicon Mac:

```sh
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh
```

<details>
<summary>"command not found", Go, and checking the download</summary>

If your terminal says `sweep: command not found`, add the install folder to
your PATH and open a new terminal:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zprofile
```

With Go installed, you can also run
`go install github.com/devloop01/sweep@latest` (it installs to `~/go/bin`).

Don't download the binary with your browser: macOS blocks it because it isn't
signed by Apple. The installer checks the download and prints its checksum;
it should match the one on the
[release page](https://github.com/devloop01/sweep/releases/latest).

</details>

## Use

Go to the folder that holds your projects and run `sweep`:

```sh
cd ~/Documents/Code
sweep
```

Pick folders with space, press `d`, and confirm. The folders go to the
Trash, so nothing is lost if you change your mind.

Panels at the top show your disk, how much sweep can free, and where the
space is going. Press `2` for a second tab with your package managers'
global caches (see [Package caches](#package-caches)), and `1` to go back.

| Key | Does |
|---|---|
| `space` or `enter` | select a folder |
| `d` | move the selected folders to the Trash, or clear the selected caches (sweep asks first) |
| `1` `2` | switch between the folders tab and the package caches tab |
| `t` | group folders by project |
| `s` | sort by size, name, or last change |
| `?` | show every key |
| `q` | quit |

The mouse works too. sweep remembers your view and sort order for next time.

To just see what's there, or clean everything without asking:

```sh
sweep -n     # list only, delete nothing
sweep -y     # move everything in the list to the Trash
```

## Getting a folder back

Open the Trash in Finder and drag the folder back where it was. Finder's
Put Back doesn't know about folders sweep moved, so each one is named after
the place it came from: `Code › web › node_modules` came from
`web/node_modules` in your `Code` folder. Rename it back to the last part
(`node_modules`) once it's in place.

Folders in the Trash still take up space: empty the Trash to get it back.

## What it deletes

Only folders your tools rebuild for free, with `npm install` or your next
dev, build, cargo or test run:

- `node_modules`
- framework caches like `.next`, `.svelte-kit`, `.turbo` and `.nuxt`
- Python caches like `.pytest_cache` and `.mypy_cache`
- build output (`dist`, `build`, `out`, Rust's `target`), but only when your
  `.gitignore` lists it

<details>
<summary>The full list, and why build folders need .gitignore</summary>

- **Dependencies:** `node_modules`
- **Framework caches:** `.next` `.svelte-kit` `.turbo` `.nuxt` `.output`
  `.astro` `.vite` `.parcel-cache` `.angular` `.docusaurus` `.nx` `.vinxi`
  `storybook-static` and `.vercel/output`
- **Python caches:** `.pytest_cache` `.mypy_cache` `.ruff_cache`
- **Build output:** `dist` `build` `out` `target`

Projects sometimes keep real files in build folders: a committed `dist`,
scripts in `build`, or new files you haven't committed yet. So sweep deletes
them only in a git repo whose ignore rules name them, with nothing inside
tracked. Anything else is skipped, and sweep tells you how many.

sweep never deletes things that would make you set up a project again, like
`.vercel`, `.netlify` or `.wrangler`.

</details>

## Package caches

Package managers keep one shared download cache in your home folder, used
by every project. sweep lists the ones it finds in its own tab (press `2`
or click it), with what clearing each would free:

| Cache | Cleared with |
|---|---|
| npm | `npm cache clean --force` |
| bun | `bun pm cache rm` |
| Go build cache | `go clean -cache` |
| Go modules | `go clean -modcache` |
| Homebrew | `brew cleanup --prune=all` (also removes old versions of installed formulae, as Homebrew does every 30 days) |
| pip | `pip3 cache purge` |
| uv | `uv cache clean` |

Caches don't go to the Trash: sweep runs the tool's own command, which
knows what's safe to remove, and the space comes back at once. Whatever
your next install needs downloads again. Each tab acts only on its own
list: `d` in the folders tab never clears a cache, and `d` in the caches
tab never moves a folder. `-n` and `-y` leave caches out.

<details>
<summary>Why pnpm's store isn't listed</summary>

On a Mac, pnpm copies packages into projects as APFS clones, so sweep
can't tell which store files a project still uses. Its size would promise
gigabytes that `pnpm store prune` doesn't free. Run that yourself when you
want to.

</details>

## Safety

- sweep asks before moving anything, unless you pass `-y`.
- It never deletes folders: they go to the Trash, so you can get one back.
  Package caches are the one exception: their own tool clears them, after
  you confirm.
- It only deletes folders with the names above, never your code.
- It won't run in your home folder, `~/Library`, system folders, or with
  `sudo`.
- It doesn't follow symlinks, never touches `.git` folders, apps or the
  Trash, and stays on your Mac's drive.

To keep sweep out of a folder, put an empty `.sweepignore` file in it.

## Customize

To delete more folder names, or stop deleting some, create
`~/.config/sweep/targets.conf`:

```
# also delete these
.cache
coverage

# never delete these
!out
```

<details>
<summary>More on targets.conf</summary>

It works like a `.gitignore`: one folder name per line, `!` in front of a
name to stop deleting it, and `parent/child` to delete `child` only inside
`parent` (like `.vercel/output`). If the file has a mistake, sweep tells you
the line and doesn't run.

</details>

## Upgrade or uninstall

```sh
sweep upgrade                                   # sweep also tells you when one is out
rm "$(which sweep)" && rm -rf ~/.local/state/sweep   # uninstall
```

<details>
<summary>Why the freed space can look smaller than shown</summary>

Sizes are real disk usage. The space comes back when you empty the Trash.
With pnpm, the space you get back can be smaller,
because pnpm keeps one shared copy of every package. On a Mac with Time
Machine snapshots, freed space can also show up a little later.

</details>

---

Working on sweep itself? See [DEVELOPMENT.md](DEVELOPMENT.md).
[MIT License](LICENSE).
