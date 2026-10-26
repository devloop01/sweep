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

### 1. One-time setup

The repo is private, so you need access to it and a GitHub login on your Mac.

1. Ask for read access to [`devloop01/sweep`](https://github.com/devloop01/sweep).
2. Install the GitHub CLI and sign in. `gh auth setup-git` lets git (and so
   Go) use that login:

   ```sh
   brew install gh
   gh auth login
   gh auth setup-git
   ```

### 2a. Install with Go (recommended)

This builds sweep on your Mac, so macOS doesn't quarantine it, and updating is
one command.

1. Install Go 1.27 or later: `brew install go`, then check with `go version`.
2. Tell Go the repo is private, so it fetches it from GitHub directly instead
   of through the public module proxy. Keep the quotes; zsh and fish reject the
   `*` without them.

   ```sh
   go env -w 'GOPRIVATE=github.com/devloop01/*'
   ```

3. Install:

   ```sh
   go install github.com/devloop01/sweep@latest
   ```

4. `go install` puts the binary in `~/go/bin`. If `sweep --version` says
   "command not found", add that folder to your PATH, then open a new terminal:

   ```sh
   # zsh (the macOS default)
   echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zprofile
   # fish
   fish_add_path ~/go/bin
   ```

### 2b. Or download the prebuilt binary (no Go needed)

Download it with `gh`, not a browser. macOS quarantines unsigned binaries that
browsers and Slack download, and Gatekeeper then blocks them. Files that `gh`
downloads aren't quarantined. Don't disable Gatekeeper to get around this.
(Signing and notarising the binary would fix this, but needs an Apple
Developer account, which this project doesn't use yet.)

```sh
cd "$(mktemp -d)"
gh release download -R devloop01/sweep -p sweep-darwin-arm64 -p SHA256SUMS
shasum -a 256 -c SHA256SUMS        # must print "sweep-darwin-arm64: OK"
chmod +x sweep-darwin-arm64
mkdir -p ~/.local/bin
mv sweep-darwin-arm64 ~/.local/bin/sweep
```

If `sweep --version` says "command not found", add `~/.local/bin` to your PATH
the same way as above (`$HOME/.local/bin` instead of `$HOME/go/bin`).

### 3. Check it and do a first run

```sh
sweep --version                     # e.g. v0.2.0
sweep --dry-run ~/Documents/Code    # lists what it would delete, deletes nothing
```

### Update or uninstall

- **Update:** run the same install command again: `go install
  github.com/devloop01/sweep@latest`, or repeat the download steps.
- **Uninstall:** `rm ~/go/bin/sweep` (Go) or `rm ~/.local/bin/sweep` (binary).
  Nothing else is installed.

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
