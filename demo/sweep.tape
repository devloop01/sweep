# The README recording. Run it with make demo; see https://github.com/charmbracelet/vhs

Output demo/sweep.gif

Set Shell "bash"
Set FontSize 15
Set Width 1000
Set Height 520
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 70ms

# Fresh state (so the default view), a short prompt, and the demo folder.
Hide
Type "export XDG_STATE_HOME=$(mktemp -d) PATH=$PWD/demo/bin:$PATH PS1='$ ' && cd demo/projects && clear"
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

# Delete the selection, through the confirm dialog.
Enter
Sleep 1.5s
Left
Sleep 600ms
Enter
Sleep 2.5s

Type "q"
Sleep 2s
