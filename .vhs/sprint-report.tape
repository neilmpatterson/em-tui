Output docs/sprint-report.gif

Set Shell "bash"
Set Width 1200
Set Height 600
Set FontSize 13
Set Theme "Catppuccin Mocha"

Type "go run ./cmd/em-tui --demo"
Enter
Sleep 2s

# Select team → Sprint Report
Down
Enter
Sleep 500ms
# Navigate to Sprint Report option (3rd item)
Down
Down
Down
Enter
Sleep 2.5s

# Scroll through sprints
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Enter
Sleep 2s

# Scroll the report
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms

Sleep 3s
