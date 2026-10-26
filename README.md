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

macOS only (Apple Silicon and Intel). Building for any other OS fails with
"build constraints exclude all Go files".

**Recommended:** build it locally with Go 1.27+:

```sh
go install github.com/devloop01/sweep@latest
```

The repo is private, so do this once first:

1. Get read access to `devloop01/sweep` on GitHub.
2. Tell Go to fetch it directly instead of through the public proxy:
   `go env -w GOPRIVATE=github.com/devloop01/*`
3. Make sure git can authenticate to GitHub over HTTPS. With the GitHub CLI,
   `gh auth login` followed by `gh auth setup-git` does it.

It lands in `$(go env GOPATH)/bin`. Because it's built on your machine, macOS
doesn't quarantine it.

**Prebuilt binary:** `make release` produces `dist/sweep-darwin-arm64` (Apple
Silicon) and `dist/sweep-darwin-amd64` (Intel). These binaries aren't signed. If
you download one through a browser or Slack, macOS quarantines it and Gatekeeper
blocks it. Signing and notarisation need an Apple Developer account, which this
project doesn't use yet. Until then, use `go install`. Don't disable Gatekeeper.

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

- Refuses to run in `/`, your home folder or any folder above it (such as
  `/Users`), `~/Library`, `~/Applications`, `/System`, `/Applications`,
  `/Library`, `/opt`, `/usr`, `/private` (this includes `/tmp` and `/var`),
  `/Volumes` itself, and `*.app` bundles. Paths are compared ignoring case,
  like APFS does.
- Never follows symlinks, and never looks inside `.git` folders or `*.app`
  bundles. Electron apps ship their own `node_modules`. This also means a
  project folder named like a domain, such as `acme.app`, is skipped entirely,
  and refused if you pass it as DIR. Rename it, or clean it by hand.
- Only real folders are deleted. A symlink or file named `node_modules` is
  skipped.
- Each folder is checked again right before deletion. It must still be inside
  DIR, reached without going through a symlink, and still a real target folder.

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
make release   # dist/sweep-darwin-{arm64,amd64}
```

Tests only touch folders under `t.TempDir()`.
