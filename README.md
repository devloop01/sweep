# sweep

[![test](https://github.com/devloop01/sweep/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/devloop01/sweep/actions/workflows/test.yml)

Free up disk space by deleting `node_modules` and framework build caches
(`.next`, `.svelte-kit`, `.turbo`, …) across all your projects at once.
sweep lists what it found, biggest first, asks once, then deletes.

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

Everything it deletes by default comes back with `npm install` / `pnpm install`
or your next dev or build run.

## Install

For Apple Silicon Macs.

**With Go** (1.27 or later):

```sh
go install github.com/devloop01/sweep@latest
```

**Without Go:**

```sh
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh
```

The script downloads the binary from the latest release, checks its checksum,
and installs it to `~/.local/bin`. For a specific release use
`… | sh -s -- v0.2.0`; for another folder, `… | SWEEP_INSTALL_DIR=/some/dir sh`.

If `sweep` isn't found afterwards, add `~/go/bin` (Go) or `~/.local/bin`
(script) to your PATH and open a new terminal:

```sh
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zprofile   # zsh
fish_add_path ~/go/bin                                     # fish
```

> The binary isn't signed by Apple. If you download it with a browser, macOS
> quarantines it and won't run it. Use one of the commands above instead, and
> don't disable Gatekeeper.

## Upgrade

Run the command you installed with again. It replaces the old binary.

```sh
go install github.com/devloop01/sweep@latest                                    # installed with Go
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh  # installed with the script
sweep --version
```

Not sure which you used? Run `which sweep`: `~/go/bin/sweep` means Go,
`~/.local/bin/sweep` means the script.

## Uninstall

sweep is a single file and keeps no settings or caches, so removing the binary
removes everything:

```sh
rm "$(which sweep)"
```

If you added `~/go/bin` or `~/.local/bin` to your PATH only for sweep, you can
also delete that line from `~/.zprofile` (zsh) or run
`set -U fish_user_paths (string match -v ~/go/bin $fish_user_paths)` (fish,
with the folder you added).

## Usage

Run it inside the folder that holds your projects. Start with a dry run:

```sh
cd ~/Documents/Code
sweep -n        # show what would be deleted, delete nothing
sweep           # show, ask, delete
```

```
sweep [flags] [DIR]    DIR defaults to the current folder

-n, --dry-run   only list what would be deleted
-y, --yes       delete without asking
-b, --build     also delete dist/ build/ out/
-d, --deploy    also delete .vercel .netlify .wrangler
-h, --help      show help
    --version   print the version
```

Only `y` or `yes` at the prompt deletes. Anything else cancels, and so does
piped input unless you pass `--yes`.

## What gets deleted

| | Folders |
|---|---|
| **Always** | `node_modules` `.svelte-kit` `.turbo` `.next` `.nuxt` `.output` `.astro` `.vite` `.parcel-cache` `.angular` `.docusaurus` `.nx` `storybook-static`, and `.vercel/output` (the rest of `.vercel` is kept) |
| **`--build`** | `dist` `build` `out`. Opt-in because some projects commit these or keep real files in them. |
| **`--deploy`** | All of `.vercel` `.netlify` `.wrangler`. Opt-in because they aren't just cache: you'll need `vercel link` again, and local D1/KV/R2/Durable Objects data in `.wrangler/state` is lost. |

These are the built-in lists, from
[`internal/sweep/targets.conf`](internal/sweep/targets.conf). You can change
them; see [Customize](#customize).

## Customize

To change which folders sweep targets, create `~/.config/sweep/targets.conf`
(or `$XDG_CONFIG_HOME/sweep/targets.conf`). It uses the same format as the
[built-in file](internal/sweep/targets.conf):

```
# The built-in list, minus .output, plus .cache
[default]
node_modules
.svelte-kit
.turbo
.next
.nuxt
.astro
.vite
.parcel-cache
.angular
.docusaurus
.nx
storybook-static
.cache

# Don't touch .vercel/output
[nested]
```

- Each `[section]` you include **replaces** that built-in list. Sections you
  leave out keep the defaults, so include only the ones you want to change.
  An empty section means "none".
- The sections are `[default]` (always), `[nested]` (always, written
  `parent/child`, deletes only the child), `[build]` (with `--build`) and
  `[deploy]` (with `--deploy`).
- One exact folder name per line, matched case-sensitively. Paths, wildcards,
  spaces, `.`, `..`, `.git` and `*.app` are rejected. A mistake stops sweep
  with the line number before it scans anything.
- When your file is in use, sweep prints `Using targets from …`, so you always
  know which lists a run used.

The config only changes which folder names count as targets. None of the
safety rules below can be changed, and sweep still shows the list and asks
before deleting.

## Safety

sweep deletes folders, so it errs on the side of skipping them.

- **It won't run in** your home folder or any folder above it, `~/Library`,
  `~/Applications`, hidden folders in your home (`~/.nvm`, `~/.vscode`, …),
  system folders (`/System`, `/Library`, `/usr`, `/opt`, `/private`, which
  covers `/tmp`), `/Volumes` itself, or inside an `.app`. It also refuses to
  run as root (`sudo`).
- **It never follows symlinks.** A symlink named `node_modules` is skipped,
  and so is a file with a target's name.
- **It skips `.git` folders and `.app` bundles.** Electron apps ship their own
  `node_modules`. This also skips a project folder named like `acme.app`.
- **It stays on one drive.** Network shares, disk images and other volumes
  mounted inside the folder are left alone.
- **It checks every folder again right before deleting it.** Deletion can't
  leave the folder you started in, even if something is swapped for a symlink
  at the last moment.
- **It ignores anything you type while it's scanning**, so an early `y` can't
  confirm a list you haven't seen.

## Sizes

Sizes are actual disk usage, like `du`, and a hard-linked file is counted
once. With pnpm, `node_modules` is mostly hard links into pnpm's store, which
keeps the files, so the space freed can be much lower than the size shown.

## Development

```sh
make test      # go vet + go test -race
make build     # ./bin/sweep
make release   # dist/sweep-darwin-arm64
```

Tests only touch folders under `t.TempDir()`.

## License

[MIT](LICENSE)
