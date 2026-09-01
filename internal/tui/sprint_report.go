package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

type SprintReportModel struct {
	team       config.TeamConfig
	jiraClient *jira.Client
}

func NewSprintReport(cfg *config.Config, teamIdx int, jiraClient *jira.Client) SprintReportModel {
	team := config.TeamConfig{}
	if teamIdx < len(cfg.Teams) {
		team = cfg.Teams[teamIdx]
	}
	return SprintReportModel{team: team, jiraClient: jiraClient}
}

func (m SprintReportModel) Init() tea.Cmd { return nil }

func (m SprintReportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if key.String() == "esc" || key.String() == "q" {
			return m, func() tea.Msg { return NavigateTo{Screen: ScreenMainMenu} }
		}
	}
	return m, nil
}

func (m SprintReportModel) View() string {
	return titleStyle.Render("Sprint Report — "+m.team.Name) + "\n\n" +
		lipgloss.NewStyle().Faint(true).Render("Coming soon — will show sprint velocity, completed issues, and carry-over.") +
		"\n\n" + lipgloss.NewStyle().Faint(true).Render("esc back")
}
