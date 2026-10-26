# sweep

Free up disk space by deleting `node_modules` and build caches (`.next`,
`.svelte-kit`, `.turbo`, …) from all your projects at once.

```
$ cd ~/Documents/Code
$ sweep
Scanning /Users/me/Documents/Code ...
    1.2 GB  agency/web/node_modules
  340.5 MB  agency/web/.svelte-kit
   12.0 KB  site/.vercel/output
----
3 folders, 1.5 GB
Delete all of these? [y/N] y
Deleted 3 folders, freed about 1.5 GB.
```

Everything it deletes comes back with `npm install` / `pnpm install` or your
next dev or build run.

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

Go to the folder that holds your projects, look first, then clean:

```sh
cd ~/Documents/Code
sweep -n     # dry run: shows what would be deleted, deletes nothing
sweep        # shows the list, asks once, then deletes
```

| Flag | What it does |
|---|---|
| `-n`, `--dry-run` | Only show what would be deleted |
| `-y`, `--yes` | Delete without asking |
| `-b`, `--build` | Also delete `dist`, `build` and `out` folders |
| `-d`, `--deploy` | Also delete `.vercel`, `.netlify` and `.wrangler` folders |

You can also pass a folder instead of using `cd`: `sweep -n ~/Documents/Code`.

## What gets deleted

| | |
|---|---|
| **Always** | `node_modules` `.next` `.svelte-kit` `.turbo` `.nuxt` `.output` `.astro` `.vite` `.parcel-cache` `.angular` `.docusaurus` `.nx` `storybook-static`, and `.vercel/output` |
| **With `--build`** | `dist` `build` `out`. Some projects keep real files in these, so check the list first. |
| **With `--deploy`** | `.vercel` `.netlify` `.wrangler`. You'll need to run `vercel link` again, and local Cloudflare dev data (D1, KV, R2) is lost. |

## Customize

To choose your own folders, create `~/.config/sweep/targets.conf`. For
example, to also delete `.cache` folders with `--build`:

```
[build]
dist
build
out
.cache
```

A section you write replaces that built-in list, so list everything you
want in it. Sections you leave out stay as they are. The sections are
`[default]`, `[build]`, `[deploy]` and `[nested]`; see the
[built-in lists](internal/sweep/targets.conf) for all of them. If the file has
a mistake, sweep tells you the line and doesn't run.

## Skip a folder

To keep sweep out of a folder, put an empty `.sweepignore` file in it:

```sh
touch ~/Documents/Code/my-project/.sweepignore
```

sweep won't look inside that folder at all, so nothing in it is ever deleted.

## Is it safe?

sweep only deletes folders with the names above, and always shows you the
list first. It also:

- won't run in your home folder, `~/Library`, system folders, or with `sudo`
- never follows shortcuts (symlinks), and never touches `.git` folders or apps
- stays on your Mac's drive: it won't go into network shares or USB drives
  mounted inside the folder

Sizes are real disk usage. With pnpm, the freed space can be smaller than
shown, because pnpm keeps a shared copy of every package.

## Upgrade or uninstall

- **Upgrade:** run the install command again.
- **Uninstall:** `rm "$(which sweep)"`. sweep is a single file and leaves
  nothing else behind.

---

Working on sweep itself? See [DEVELOPMENT.md](DEVELOPMENT.md).
[MIT License](LICENSE).
