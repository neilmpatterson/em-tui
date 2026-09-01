package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

type PerfReviewModel struct {
	team       config.TeamConfig
	jiraClient *jira.Client
	memberIdx  int
}

func NewPerfReview(cfg *config.Config, teamIdx int, jiraClient *jira.Client, memberIdx int) PerfReviewModel {
	team := config.TeamConfig{}
	if teamIdx < len(cfg.Teams) {
		team = cfg.Teams[teamIdx]
	}
	return PerfReviewModel{team: team, jiraClient: jiraClient, memberIdx: memberIdx}
}

func (m PerfReviewModel) Init() tea.Cmd { return nil }

func (m PerfReviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if key.String() == "esc" || key.String() == "q" {
			return m, func() tea.Msg { return GoBack{} }
		}
	}
	return m, nil
}

func (m PerfReviewModel) View() string {
	name := "Unknown"
	if m.memberIdx < len(m.team.Members) {
		name = m.team.Members[m.memberIdx].DisplayName
	}
	return titleStyle.Render("Perf Review — "+name) + "\n\n" +
		lipgloss.NewStyle().Faint(true).Render("Coming soon — will aggregate Jira history and git activity over the review period.") +
		"\n\n" + lipgloss.NewStyle().Faint(true).Render("esc back")
}
