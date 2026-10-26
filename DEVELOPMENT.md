# Developing sweep

[![test](https://github.com/devloop01/sweep/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/devloop01/sweep/actions/workflows/test.yml)

sweep is a small Go program with no dependencies outside the standard library.
It builds for Apple Silicon Macs only (`//go:build darwin && arm64`).

## Build and test

Needs Go 1.27 or later.

```sh
make test      # go vet + go test -race
make build     # ./bin/sweep
make release   # dist/sweep-darwin-arm64
```

Tests only touch folders under `t.TempDir()`. The mount test attaches small
disk images with `hdiutil` and skips itself if that isn't possible.

To use your local build as `sweep`, run `go install .`. That replaces whatever
is in `~/go/bin/sweep`.

### Try it on a throwaway folder

sweep refuses to run in `/tmp` (it's under `/private`), so make the folder
somewhere in your home:

```sh
mkdir -p ~/sweep-playground/app/node_modules/pkg ~/sweep-playground/app/.next ~/sweep-playground/site/dist
echo keep > ~/sweep-playground/app/package.json
cd ~/sweep-playground && ~/path/to/sweep/bin/sweep -n -b
```

## Layout

| File | What's in it |
|---|---|
| `main.go` | Flags, prompt, output and exit codes |
| `internal/sweep/targets.conf` | Built-in target lists, embedded in the binary |
| `internal/sweep/targets.go` | Parsing and validating `targets.conf` and the user's config |
| `internal/sweep/guard.go` | Which folders sweep refuses to run in |
| `internal/sweep/scan.go` | Walking, matching, sizes, and the re-check before delete (`Vet`) |
| `internal/sweep/git.go` | Whether a target may hold git-tracked files |
| `internal/sweep/remove.go` | Deleting the vetted folders through an `os.Root` |
| `install.sh` | The `curl … \| sh` installer |

## Safety rules

Every rule has tests. When in doubt, sweep skips a folder rather than deleting it.

- **Refused folders.** `/`, the home folder and every folder above it (such as
  `/Users`), `~/Library`, `~/Applications`, hidden folders directly in home
  (`~/.nvm`, `~/.vscode`, …), `/System`, `/Applications`, `/Library`, `/opt`,
  `/usr`, `/private` (so `/tmp` and `/var`), `/Volumes` itself, and anything in
  a `*.app`. Paths are compared by component and ignoring case, like APFS.
  "Home" means both `$HOME` and the home folder macOS has on record, since
  `$HOME` can be changed. Running as root is refused, because under `sudo`
  both can point at root's home.
- **No symlinks.** The walk never follows them, and a symlink or file with a
  target's name is skipped.
- **Never inside** `.git` folders or `*.app` bundles.
- **Git-tracked folders are kept.** A target inside a git repo is kept if
  `git ls-files` lists anything in it (a committed `dist`, scripts in
  `build`), or if git can't answer: it isn't installed, times out, or the
  repo is broken. Kept folders are printed. Outside a repo, git isn't run.
  Checked during the scan and again before deleting.
- **`.sweepignore`.** A folder containing an entry with this name is never
  entered, and a target containing one is skipped. The walk skips marked
  folders, and the re-check before delete looks for a marker at every level
  from DIR down, so a marker added after the scan still counts.
- **One volume.** The walk doesn't cross mount points, and a target with
  anything mounted inside it is skipped.
- **Prune on match.** sweep never looks inside a folder it already matched, so
  nothing is listed twice.
- **Re-check before delete.** Each folder must still be strictly inside DIR,
  reached without going through a symlink, a real directory on the same
  volume, and still a target. Deletion then goes through an `os.Root` opened
  before the scan, so a folder swapped for a symlink at the last moment can't
  lead outside DIR. Known limit: inside DIR, that race could still redirect a
  delete to another target-named folder in DIR (see `remove.go`).
- **Prompt.** Only `y`/`yes` proceeds. Piped input cancels unless `--yes` is
  given, and anything typed during the scan is discarded first.
- **Output.** Control, format and line-separator characters in names print as
  `?`, so a folder name can't fake the list.

### Config validation

The built-in `targets.conf` and a user's `~/.config/sweep/targets.conf` use
the same flat, `.gitignore`-like format. Each line is a folder name or
`parent/child`; a trailing `/` is ignored. The user's lines add names, and
`!name` removes one. Paths, a leading `/`, wildcards, spaces, control
characters, `.`, `..`, `.git` and `*.app` are rejected, and so is a `!name`
that isn't in the list, so a typo can't silently leave a folder deletable. Any
mistake stops the run with the line number before scanning. A nested target is
dropped when its parent is a target too, so adding `.vercel` doesn't list
`.vercel/output` twice.

What goes in the built-in list: only folders tools regenerate for free.
Nothing that would make someone set a project up again (`.vercel`,
`.netlify`, `.wrangler`) is a default.

## Sizes

Sizes are allocated disk blocks (`Stat_t.Blocks * 512`, like `du`), not
apparent size. A file hard-linked into several targets is counted once, against
the first target in path order, so totals and order are the same every run.

## install.sh

Options for the installer:

```sh
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | sh -s -- v0.3.0   # a specific release
curl -fsSL https://raw.githubusercontent.com/devloop01/sweep/main/install.sh | SWEEP_INSTALL_DIR=/some/dir sh
```

It checks the download against `SHA256SUMS`, times out and retries stalled
downloads, and installs to `~/.local/bin` by default. Files downloaded with
`curl` aren't quarantined by macOS, which is why the binary runs even though
it isn't signed. Signing and notarising would need an Apple Developer account.

CI runs the installer on every push to make sure it still works.

The workflows pin each action to a commit SHA. Dependabot opens a pull request
each month when one of them has a new version.

## Releasing

Push a tag and GitHub Actions does the rest: it runs the tests, builds
`sweep-darwin-arm64`, checks that it reports the tag's version, writes
`SHA256SUMS`, attests the build, and publishes the release. The release notes
list the commit messages since the last release, plus install commands, so
write commit subjects you'd be happy to see there. You can edit the notes on
GitHub afterwards.

```sh
git tag -a v0.4.0 -m v0.4.0 && git push origin v0.4.0
```

A tag with a `-`, like `v0.4.0-rc.1`, becomes a pre-release, which the
installer and `go install …@latest` ignore. Use one to try a release first,
then delete it with `gh release delete v0.4.0-rc.1 --cleanup-tag`.

### Verifying a release binary

```sh
gh attestation verify ~/.local/bin/sweep -R devloop01/sweep
```

This confirms the binary was built by this repo's release workflow from the
tagged source.
