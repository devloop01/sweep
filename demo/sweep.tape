# The README recording. Run it with make demo; see https://github.com/charmbracelet/vhs

Output demo/sweep.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 520
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 70ms

# Fresh state (so the default view), a home of its own (so its Trash is
# demo/home/.Trash, not yours, and no package caches are found), a short
# prompt, and the demo folder. The cache overrides are unset so none of
# your real caches can show up and be cleared by the d below.
Hide
Type "unset GOCACHE GOMODCACHE GOPATH npm_config_cache BUN_INSTALL_CACHE_DIR HOMEBREW_CACHE PIP_CACHE_DIR UV_CACHE_DIR XDG_CACHE_HOME"
Enter
Type "mkdir -p demo/home && export HOME=$PWD/demo/home XDG_STATE_HOME=$(mktemp -d) PATH=$PWD/demo/bin:$PATH PS1='$ ' && cd demo/projects && clear"
Enter
Show

Sleep 500ms
Type "sweep"
Sleep 300ms
Enter
Sleep 2s

# Pick two folders.
Space
Sleep 500ms
Down
Sleep 300ms
Down
Sleep 300ms
Space
Sleep 1s

# Group by project, hop to the next one, collapse it.
Type "t"
Sleep 1.5s
Tab
Sleep 600ms
Tab
Sleep 600ms
Left
Sleep 1s
Type "c"
Sleep 1.2s
Type "c"
Sleep 1s

# Sort by last change.
Type "s"
Sleep 700ms
Type "s"
Sleep 1.5s

# Move the selection to the Trash, through the confirm dialog.
Type "d"
Sleep 1.5s
Left
Sleep 600ms
Enter
Sleep 2.5s

Type "q"
Sleep 2s
