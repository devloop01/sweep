# sweep

Free up disk space by deleting `node_modules` and build caches (`.next`,
`.svelte-kit`, `.turbo`, …) from all your projects at once.

```
$ cd ~/Documents/Code
$ sweep
sweep  3 folders, 1.5 GB
↑↓ move   space select   a all   enter delete   q quit

  [x] agency/web/node_modules                1.2 GB
>     agency/web/.svelte-kit                deleted
  [ ] site/.vercel/output                   12.0 KB

Selected 1 folder, 1.2 GB · enter deletes them
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

Go to the folder that holds your projects and run `sweep`:

```sh
cd ~/Documents/Code
sweep
```

The list opens right away and fills in while sweep scans, biggest first.
Move with ↑/↓ (or j/k), select folders with **space** (`a` selects all),
and press **enter** to delete them. With nothing selected, enter deletes the
highlighted folder. `q` quits.

To just look, or to clean everything at once:

```sh
sweep -n     # only print the list, delete nothing
sweep -y     # delete everything in the list, no questions
```

In a long printed list, the many tiny folders (under 1 MB each) are shown as
one line, so the ones that matter stand out. You can also pass a folder
instead of using `cd`: `sweep -n ~/Documents/Code`.

## What gets deleted

Only folders that are safe to throw away, because your tools rebuild them
for free:

- **Dependencies:** `node_modules`
- **Framework caches:** `.next` `.svelte-kit` `.turbo` `.nuxt` `.output`
  `.astro` `.vite` `.parcel-cache` `.angular` `.docusaurus` `.nx` `.vinxi`
  `storybook-static` and `.vercel/output`
- **Build output:** `dist` `build` `out`, but only in a git repo where git
  confirms nothing inside is tracked. Projects sometimes keep real files in
  these (a committed `dist`, scripts in `build`), so outside git, or when git
  tracks files in them, sweep skips them and says how many it skipped.

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

sweep won't look inside that folder at all, so nothing in it is ever deleted.

## Is it safe?

sweep only deletes folders with the names above, and always shows you the
list first. It also:

- won't run in your home folder, `~/Library`, system folders, or with `sudo`
- never follows shortcuts (symlinks), and never touches `.git` folders or apps
- skips any folder that git tracks files in
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
