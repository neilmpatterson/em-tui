Output docs/standup.gif

Set Shell "bash"
Set Width 120
Set Height 36
Set FontSize 14
Set Theme "Catppuccin Mocha"

Type "go run ./cmd/em-tui --demo"
Enter
Sleep 2s

# Select team → Standup
Down
Enter
Sleep 500ms
# Standup is second option
Down
Enter
Sleep 3s

# Navigate between members
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Up
Sleep 400ms

Sleep 3s
