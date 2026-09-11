# em-tui

A terminal dashboard for engineering managers. Connects to Jira and git to
surface the context you need for standups, 1:1s, sprint reviews, and perf
calibration — without leaving the terminal.

![em-tui demo](docs/em-tui-demo.gif)

## Screens

### Main Menu

Three-level navigation: teams → team actions / members → person actions.
Use arrow keys to move, `enter` to select, `esc` or `q` to go back.

**Team actions:** Standup prep, Sprint report, Modify team, Delete team

**Member actions:** 1:1 prep, Perf review notes, Stats, Delete member

---

### Standup Prep

Per-team view. Shows every team member's current Jira activity: open tickets
by status, recently completed work, and any tickets stuck in watch statuses
(Code Review, Testing, etc.). Designed to be the first thing you open before
a standup.

Key bindings: `j`/`k` or `↑`/`↓` to move between members, `q` to go back.

---

### Sprint Report

Select any sprint from history. Shows:

- Velocity bar chart across recent sprints
- Completed issues table with assignee, priority, and story points
- Not-completed issues with current status
- Tickets added mid-sprint (marked `+`)
- SP totals: planned, completed, incomplete

Key bindings: `j`/`k` to navigate sprint list, `enter` to load a report,
`j`/`k` to scroll the report, `esc` to return to sprint list.

---

### 1:1 Prep

Auto-generated talking points backed by 90-day Jira changelogs and git
history. Points include:

- **Cycle time** — p50/p90 days from first move to done
- **Phase breakdown** — where time is spent (Dev, Code Review, QA, etc.)
- **QA bounces** — tickets that went back from Testing
- **Velocity trend** — tickets done per week over 4 weeks
- **Slowest tickets** — issues that took more than 2× the median
- **Stale WIP** — open tickets untouched for 14+ days
- **Rework rate** — tickets that hit Bug Fix Needed

Drill into any talking point to see the underlying tickets (`enter`). Open
a ticket in your browser directly from the drill-down (`enter` on a ticket
row, if a URL is configured).

Key bindings: `j`/`k` to move between points, `enter` to drill in/open,
`esc` to collapse.

---

### Developer Stats

18-month dashboard for a single team member. Designed for perf reviews and
calibration conversations. Sections:

- **KPI strip** — tickets done, bugs, QA bounce rate, cycle time p50/p90
- **Ticket breakdown** — by type (Story, Bug, Task) and priority
- **Velocity** — sprint-by-sprint bar chart
- **Cycle time** — phase bars showing where time is spent
- **Codebase heatmap** — top directories by commit count
- **Sprint history** — table of every sprint the member participated in

---

### Perf Review Notes

Placeholder screen for aggregating free-text review notes. Planned for a
future release.

---

### Setup Wizard

First-run credential setup and team builder. Steps:

1. Enter your Jira base URL
2. Enter your Jira email
3. Enter your Jira API token
4. Add teams: search members by name, add git repo paths, select a Scrum board

Config is saved to `~/.config/em-tui/config.yaml`.

---

## Install

**From source:**

```bash
git clone https://github.com/neilmpatterson/em-tui
cd em-tui
make build          # produces bin/em-tui
./bin/em-tui
```

**With go install:**

```bash
go install github.com/neilmpatterson/em-tui@latest
```

Requires Go 1.24+.

---

## Setup

First run launches the wizard automatically. You can also run it any time:

```bash
em-tui --setup
```

You'll need a Jira API token. Create one at:
`https://id.atlassian.com/manage-profile/security/api-tokens`

Keep the token out of the config file by setting an environment variable:

```bash
export JIRA_API_TOKEN=your_token
```

Then in `~/.config/em-tui/config.yaml`, set:

```yaml
jira:
  api_token: "${JIRA_API_TOKEN}"
```

---

## Config Reference

```yaml
jira:
  base_url: "https://yourcompany.atlassian.net"
  email: "you@company.com"
  api_token: "${JIRA_API_TOKEN}"    # or paste the token directly

teams:
  - name: "Platform"
    board_id: 42
    board_type: "scrum"             # or "kanban"
    members:
      - account_id: "abc123"        # Jira account ID (wizard fills this)
        display_name: "Alex Chen"
        email: "alex.chen@company.com"   # used as git --author filter
    repos:
      - "~/Projects/platform"       # tilde-expanded; supports multiple repos
    projects: ["PLAT"]              # Jira project keys for this team
    incoming_bugs: "22598"          # optional: Jira filter ID for bug triage
    security_issues: "22597"        # optional: Jira filter ID for security tickets
    watch_statuses:                 # statuses to highlight in standup
      - "Testing"
      - "Code Review"
    phases:                         # optional: custom status → phase mapping
      "Bug Fix Needed": "rework"
```

---

## Key Bindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Move down |
| `k` / `↑` | Move up |
| `enter` / `space` | Select / expand |
| `esc` / `q` | Back / close |
| `tab` | Switch focus (where applicable) |
| `g` | Jump to top |
| `G` | Jump to bottom |
| `ctrl+c` | Quit |

---

## Demo Mode

Run without a Jira account using pre-baked fake data:

```bash
em-tui --demo
```

This loads a fictional "Acme Corp / Platform" team with realistic sprint
history, changelogs, and developer stats. No network calls are made.

---

## Requirements

- Go 1.24+ (to build from source)
- Jira Cloud account with API token
- Git (for commit history features — optional)
