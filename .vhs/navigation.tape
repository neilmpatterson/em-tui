Set Shell zsh
Set Width 1200
Set Height 600
Set FontSize 13
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
