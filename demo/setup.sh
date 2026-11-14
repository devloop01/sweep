#!/bin/sh
# Builds demo/projects, a made-up projects folder for the README recording
# (make demo). It writes about 120 MB of zeros; sweep moves some of it to
# demo/home/.Trash on camera and make demo removes it all.
set -eu
cd "$(dirname "$0")"
rm -rf projects
mkdir projects
cd projects

# file PATH SIZE_KB: SIZE_KB thousand bytes of zeros (real disk use; sweep
# counts that).
file() {
	mkdir -p "$(dirname "$1")"
	dd if=/dev/zero of="$1" bs=1000 count="$2" 2>/dev/null
}
json() { mkdir -p "$1" && echo '{}' >"$1/package.json"; }

# A Turborepo with a Next.js app and a UI package.
json agency && echo '{}' >agency/turbo.json
json agency/apps/web && touch agency/apps/web/next.config.mjs
json agency/packages/ui
file agency/node_modules/.pnpm/blob 46000
file agency/apps/web/.next/cache/blob 9000
file agency/apps/web/node_modules/blob 3000
file agency/packages/ui/.turbo/blob 300

# A SvelteKit blog, untouched for a few months.
json blog && touch blog/svelte.config.js
file blog/node_modules/blob 21000
file blog/.svelte-kit/blob 2000
find blog -exec touch -t 202607140900 {} +

# An Astro site.
json site && touch site/astro.config.mjs
file site/node_modules/blob 14000
file site/.astro/blob 200
find site -exec touch -t 202610020900 {} +

# A Rust crate. target is only deleted in git, where git says it's ignored.
mkdir engine && printf '[package]\nname = "engine"\n' >engine/Cargo.toml
echo target/ >engine/.gitignore
file engine/target/debug/blob 24000
(cd engine && git init -q && git add -A &&
	git -c user.name=demo -c user.email=demo@example.com commit -qm init)

# A Python service.
mkdir api && touch api/pyproject.toml
file api/.mypy_cache/blob 6000
file api/.pytest_cache/blob 100
find api -exec touch -t 202609200900 {} +
