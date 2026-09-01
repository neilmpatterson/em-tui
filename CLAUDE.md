# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`em-tui` is an interactive terminal UI tool for engineering managers. It connects to Jira and a local git repository to help prepare for common EM tasks: standups, 1:1s, performance reviews, and sprint reports.

A setup wizard on first run walks through:
1. Jira credentials (base URL + API token)
2. Discovering direct reports via Jira user search to build a "team"
3. Selecting a Jira board (scrum or kanban)
4. Pointing to a local git repository

Config is saved to `~/.config/em-tui/config.yaml` (gitignored equivalent of bug-butler's `config.yaml`).

## Stack

- **Language**: Go (same pattern as `~/Projects/bug-butler`)
- **TUI framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) — Elm-like model/update/view architecture
- **Styling**: [Lip Gloss](https://github.com/charmbracelet/lipgloss) for layout and color
- **Jira**: `github.com/andygrunwald/go-jira`
- **Config**: `github.com/knadh/koanf` (YAML, env var interpolation, same as bug-butler)
- **CLI bootstrap**: `github.com/spf13/cobra` (one command: `em-tui`, plus `--config` flag)

## Architecture

### Bubble Tea model hierarchy

The app is a multi-screen TUI. Each screen is its own `tea.Model` composed into a root `App` model:

```
App (root model)
├── SetupWizard     — first-run credential + team setup
├── MainMenu        — top-level navigation
├── StandupView     — per-person standup prep (Jira activity + git log)
├── OneOnOneView    — talking points for a 1:1
├── SprintReport    — sprint metrics and summary
└── PerfReviewView  — review cycle notes aggregator
```

The root `App` holds a `currentScreen` field and routes `tea.Msg`s to the active screen. Screen transitions happen via a custom `NavigateTo(screen)` message.

### Data flow

Views are read-only; they never write back to Jira or git. All Jira fetches happen in `tea.Cmd`s (async, never blocking the UI loop). Git log is read via `os/exec` calling `git log` with the configured repo path — no Git library dependency needed.

### Project layout

```
cmd/em-tui/         # entry point (main.go, cobra root command)
internal/
  tui/              # Bubble Tea models: app.go, standup.go, oneonone.go, etc.
  wizard/           # first-run setup wizard model
  config/           # koanf config load/save, including team/board persisted state
  jira/             # Jira API wrapper (reuse patterns from bug-butler)
  git/              # git log reader (exec-based)
  domain/           # shared types: TeamMember, Sprint, BoardType, etc.
```

### Config shape

```yaml
jira:
  base_url: "https://yourcompany.atlassian.net"
  email: "you@company.com"
  api_token: "${JIRA_API_TOKEN}"
  board_id: 42
  board_type: "scrum"   # or "kanban"

team:
  - account_id: "abc123"
    display_name: "Alice Smith"
  - account_id: "def456"
    display_name: "Bob Jones"

git:
  repo_path: "/path/to/your/repo"
```

## Build & Test

```bash
go build -o bin/em-tui ./cmd/em-tui
go test ./...
go test ./internal/jira/...   # single package
```

Run locally (reads `~/.config/em-tui/config.yaml` or `--config` path):

```bash
go run ./cmd/em-tui
go run ./cmd/em-tui --config ./config.dev.yaml
```

## Key Patterns

- **New screen**: add a model file in `internal/tui/`, implement `Init() tea.Cmd`, `Update(tea.Msg) (tea.Model, tea.Cmd)`, `View() string`. Register it in `app.go`'s switch on `currentScreen`.
- **New Jira query**: add a function in `internal/jira/client.go` that returns a `tea.Cmd` wrapping the API call and a typed result message.
- **Team member discovery**: wizard calls Jira's user search (`/rest/api/3/user/search?query=...`) filtered by the configured manager's `accountId` via `reportingTo` or org chart — fall back to a free-text search if the Jira instance doesn't support org chart queries.
