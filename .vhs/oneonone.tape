Output docs/oneonone.gif

Set Shell "bash"
Set Width 1200
Set Height 600
Set FontSize 13
Set Theme "Catppuccin Mocha"

Type "go run ./cmd/em-tui --demo"
Enter
Sleep 2s

# Select team → first member → 1:1 Prep
Down
Enter
Sleep 500ms
Down
Enter
Sleep 500ms
# 1:1 Prep is the first option
Enter
Sleep 4s

# Scroll down through talking points
Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 500ms

# Drill into a talking point
Enter
Sleep 1.5s

# Scroll through drill-down
Down
Sleep 300ms
Down
Sleep 300ms

# Back out
Escape
Sleep 1s

Sleep 3s
