# sweep

[![test](https://github.com/devloop01/sweep/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/devloop01/sweep/actions/workflows/test.yml)

Find and delete `node_modules` and framework build/cache folders (`.svelte-kit`,
`.next`, `.turbo`, …) under a directory. Lists them biggest first with a total,
asks once, then deletes them all.

```
$ sweep ~/Documents/Code
Scanning /Users/me/Documents/Code ...
    1.2 GB  agency/web/node_modules
  340.5 MB  agency/web/.svelte-kit
   12.0 KB  site/.vercel/output
----
3 folders, 1.5 GB
Delete all of these? [y/N] y
Deleted 3 folders, freed about 1.5 GB.
```

**First time? Run with `--dry-run`.**

## Install

Works on Apple Silicon Macs only. Intel Macs aren't supported.

### With Go

Needs Go 1.27 or later (`brew install go`):

```sh
go install github.com/devloop01/sweep@latest
```

The binary goes in `~/go/bin`. Because it's built on your Mac, macOS doesn't
quarantine it.

### Without Go

```sh
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh
```

[`install.sh`](install.sh) downloads the binary from the latest release, checks
it against `SHA256SUMS`, and puts it in `~/.local/bin`.

- **A specific release:** `… | sh -s -- v0.2.0`
- **Another folder:** `… | SWEEP_INSTALL_DIR=/some/dir sh`

Download it with `curl` (or the script), not a browser. macOS quarantines
unsigned binaries that browsers and Slack download, and Gatekeeper then blocks
them. Files that `curl` downloads aren't quarantined. Don't disable Gatekeeper to
get around this. Signing and notarising the binary would fix it, but that needs
an Apple Developer account, which this project doesn't use yet.

### PATH

If `sweep --version` says "command not found", add the install folder to your
PATH (`~/go/bin` for Go, `~/.local/bin` for the script), then open a new
terminal:

```sh
# zsh (the macOS default)
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zprofile
# fish
fish_add_path ~/go/bin
```

### First run

```sh
sweep --version                     # e.g. v0.2.0
sweep --dry-run ~/Documents/Code    # lists what it would delete, deletes nothing
```

### Update or uninstall

- **Update:** run the install command again.
- **Uninstall:** `rm ~/go/bin/sweep` (Go) or `rm ~/.local/bin/sweep` (script).
  Nothing else is installed.
- **Installed from the private repo before?** You can drop that setting:
  `go env -u GOPRIVATE`.

## Usage

```
sweep [flags] [DIR]      DIR defaults to the current directory

-n, --dry-run   only list what would be deleted
-y, --yes       delete without asking
-b, --build     also include dist/ build/ out/
-d, --deploy    also include .vercel .netlify .wrangler
-h, --help      show help
    --version   print the version
```

Without `--yes`, sweep only deletes when you answer `y` or `yes`. Anything
else, including piped input, cancels.

## What gets deleted

All target lists are in [`internal/sweep/targets.go`](internal/sweep/targets.go).

- **Default:** `node_modules .svelte-kit .turbo .next .nuxt .output .astro .vite
  .parcel-cache .angular .docusaurus .nx storybook-static`, plus `.vercel/output`.
  Only the output folder goes, so `.vercel/project.json` stays.
- **`--build`:** `dist build out`. These are opt-in because projects sometimes
  commit them or keep real files in them.
- **`--deploy`:** the whole `.vercel`, `.netlify` and `.wrangler` folders. These
  aren't pure cache: you'll need to run `vercel link` again, and `.wrangler/state`
  holds local D1/KV/R2/Durable Objects dev data, which is lost.

## Safety

- Refuses to run as root (`sudo`).
- Refuses to run in `/`, your home folder or any folder above it (such as
  `/Users`), `~/Library`, `~/Applications`, hidden folders in your home such
  as `~/.nvm`, `~/.vscode` or `~/.bun` (global installs and editor extensions
  live there), `/System`, `/Applications`, `/Library`, `/opt`, `/usr`,
  `/private` (this includes `/tmp` and `/var`), `/Volumes` itself, and `*.app`
  bundles. Paths are compared ignoring case, like APFS does. "Your home
  folder" means both `$HOME` and the home folder macOS has on record, since
  `$HOME` can be changed.
- Never follows symlinks, and never looks inside `.git` folders or `*.app`
  bundles. Electron apps ship their own `node_modules`. This also means a
  project folder named like a domain, such as `acme.app`, is skipped entirely,
  and refused if you pass it as DIR. Rename it, or clean it by hand.
- Stays on DIR's volume, like `find -xdev`. A network share, disk image or
  other volume mounted inside DIR is never swept, and a target with something
  mounted inside it is skipped.
- Only real folders are deleted. A symlink or file named `node_modules` is
  skipped.
- Each folder is checked again right before deletion. It must still be inside
  DIR, reached without going through a symlink, and still a real target folder.
  The deletion itself goes through a handle to DIR opened before the scan
  (`os.Root`), so a folder swapped for a symlink at the last moment still can't
  lead it outside DIR.
- Anything typed while sweep is scanning is discarded before the prompt, so an
  early `y` and Enter can't confirm a list you haven't seen.

## Sizes

Sizes are disk usage, as `du` reports it, not apparent file size. A file that
is hard-linked into several folders is counted once. **pnpm:** `node_modules`
is mostly hard links into the pnpm store, which keeps those files after the
folder is deleted. So for pnpm projects, the space actually freed can be much
lower than the size shown.

## Development

```sh
make test      # go vet + go test -race
make build     # ./bin/sweep
make release   # dist/sweep-darwin-arm64
```

Tests only touch folders under `t.TempDir()`.
