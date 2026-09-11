Output docs/navigation.gif

Set Shell "bash"
Set Width 120
Set Height 36
Set FontSize 14
Set Theme "Catppuccin Mocha"

Type "go run ./cmd/em-tui --demo"
Enter
Sleep 2s

# Navigate to first team
Down
Sleep 500ms
Enter
Sleep 1s

# Navigate to first member
Down
Sleep 500ms
Enter
Sleep 1.5s

# Back out
Type "q"
Sleep 500ms
Type "q"
Sleep 500ms

Sleep 2s
