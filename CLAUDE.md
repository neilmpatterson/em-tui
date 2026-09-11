# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`em-tui` is an interactive terminal UI tool for engineering managers. It connects to Jira and a local git repository to help prepare for common EM tasks: standups, 1:1s, performance reviews, and sprint reports.

A setup wizard on first run walks through:
1. Jira credentials (base URL + API token)
2. Discovering direct reports via Jira user search to build a "team"
3. Selecting a Jira board (scrum or kanban)
4. Pointing to a local git repository

Config is saved to `~/.config/em-tui/config.yaml`.

## Stack

- **Language**: Go
- **TUI framework**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) — Elm-like model/update/view architecture
- **Styling**: [Lip Gloss](https://github.com/charmbracelet/lipgloss) for layout and color
- **Jira**: `github.com/andygrunwald/go-jira`
- **Config**: `github.com/knadh/koanf` (YAML, env var interpolation)
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
  jira/             # Jira API wrapper
  git/              # git log reader (exec-based)
  domain/           # shared types: TeamMember, Sprint, BoardType, etc.
```

### Config shape

Multi-team. The old flat single-team format (`team:` / `git.repo_path`) is
auto-migrated on load by `migrateLegacy`.

```yaml
jira:
  base_url: "https://yourcompany.atlassian.net"
  email: "you@company.com"
  api_token: "${JIRA_API_TOKEN}"

teams:
  - name: "Platform"
    board_id: 42
    board_type: "scrum"        # or "kanban"
    members:
      - account_id: "abc123"
        display_name: "Alice Smith"
        email: "alice@company.com"   # used as the git --author filter
    repos:
      - "~/Projects/your-repo"       # tilde is expanded in git/log.go
    projects: ["PLAT"]
    incoming_bugs: "YOUR_FILTER_ID"  # Jira filter ID
    security_issues: "YOUR_FILTER_ID"
    watch_statuses: ["Testing", "Code Review"]
    phases:                          # optional; see Flow metrics below
      "Bug Fix Needed": "rework"
```

`marshalYAML` is hand-rolled: any new `TeamConfig` field needs a matching
writer there or it silently vanishes on the next save.

### Flow metrics (1:1 screen)

The Cycle Time section is derived from Jira issue changelogs. Two rules matter:

**Status category comes from Jira, keyed by status ID, never inferred from the
name.** `/rest/api/3/status` is fetched once and cached to
`~/.local/share/em-tui/status-categories.json` (7-day TTL). Names are *not*
unique on a site: some instances have two "On Hold" statuses in different
categories and many distinct "In Progress" IDs. Name-based matching previously classified
"Ready to Merge" as Done and "Deployment In Progress" as active development.

**Phase is a finer split than the category**, and comes from the name via
`TeamConfig.EffectivePhases()` with keyword fallback. The distinction that earns
its keep is queue vs active: "Ready for QA" and "Testing" are both category
In Progress, but one is a ticket waiting and the other is a person working.
Unrecognised statuses become `PhaseUnknown` and are surfaced as a warning rather
than folded into a neighbouring bucket.

Cycle time stops at the **first** entry into a Done-category status, matching
`statusCategory IN (Done)`. If a ticket later leaves Done, the clock restarts and
the next completion counts.

Changelogs are fetched in a second phase, after both sprint reports and open
issues have landed (`maybeFetchChangelogs` gates on all three preconditions —
adding a fourth means updating that gate or the loading screen hangs forever).

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

## Gotchas worth not rediscovering

- **`lipgloss.Render` pads multi-line input.** A styled string containing `\n`
  is treated as a block and padded to equal width, which injects trailing
  whitespace that displaces whatever renders next. Keep newlines *outside*
  `Render(...)`.
- **Value-receiver `Update` copies the model.** Setting a field in `Init()` does
  not persist; set it in the constructor. To mutate from inside a value-receiver
  `Update`, use a pointer-receiver helper (`finishIfDone`, `maybeFetchChangelogs`)
  which Go addresses implicitly.
- **Viewport height:** `App.View()` joins `body, "", footer`, adding 2 lines. A
  sub-screen body is `vpHeight+3`, so `footerLines = 3` and `vpHeight = h-5`.
  Getting this wrong clips the title off the top.
- **`exec.Command` does not invoke a shell**, so `~` is passed literally. See
  `expandTilde` in `internal/git/log.go`.
- **`time.Truncate(24*time.Hour)` is wrong for date normalisation** — it
  truncates against the UTC epoch and shifts local dates. Use
  `time.Date(y, mo, d, 0,0,0,0, time.UTC)`. Two stale instances remain in
  `standup.go` (~line 344 and ~532).
- **`sort.SliceStable` for changelog transitions.** Several status items can
  share one history timestamp; an unstable sort reorders them, breaks From/To
  adjacency, and invents rework transitions.

## Unfinished / next up

**Phase 2 — git-side metrics for the 1:1 screen.** Researched and validated
against a real repo, not yet built:

- *Commit size distribution* and *commits per ticket*. Use `--shortstat` over
  `--numstat` (730KB vs 1.26MB per 90 days; numstat costs a line per file). Show
  large commits **with their subject lines** so lockfile noise is self-evidently
  dismissible rather than filtered.
- *Parsing must use explicit sentinels, not blank lines.* Merge commits emit no
  stat line **and no blank line**, so blank-delimited records silently merge two
  commits. Capturing `%b` makes it worse. Use
  `--pretty=format:%x1e%H%x1f%an%x1f%ad%x1f%s%x1f%b%x02`, split on `\x1e`, then
  `strings.LastIndex(rec, "\x02")` to divide our fields from git's diffstat.
- *Ticket regex needs a project allowlist.* `[A-Z][A-Z0-9]+-\d+` matches
  `SHA-256`, which is a confirmed live false positive in real commit bodies.
  Build the allowlist from `team.Projects` plus prefixes seen in Jira results.
  Subject keys and body keys differ: body-only refs are usually "follow-up to X"
  and must not be treated as the primary ticket.
- *Bridge metrics* (in-progress → first commit, last commit → done) need the git
  window widened from 8 weeks to 90 days to match the changelog window, and
  `--date=short` replaced with `--date=iso-strict`. Short dates parse to UTC
  midnight and produce ±1-day errors against offset-aware Jira timestamps, not
  merely lost precision. Report medians; count "committed before moving the
  ticket" separately rather than averaging negatives in.
- `git.FetchCommits` currently discards stderr: `ExitError.Error()` is just
  "exit status 128", and `oneonone.go` drops `msg.Err` entirely, so a bad repo
  path yields a silently empty report.

**Other open threads:**

- A "stats" view for historical metrics over previous months/years, as a
  counterpart to the rolling 90-day window.
- GitLab merge-request metrics (PR cycle time, review response time, PR size).
  `renderMergeRequests()` is a placeholder seam waiting on API access.
- Obsidian vault sync, config-driven.
- Tune on real data: the dominant-phase drill-down caps at 15 tickets and the
  slowest-tickets point uses 2× median — both are guesses.
- No tests exist outside `internal/tui/` and `internal/config/`.
