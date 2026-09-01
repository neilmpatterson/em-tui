package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/git"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

type OneOnOneModel struct {
	team           config.TeamConfig
	jiraClient     *jira.Client
	memberIdx      int
	issuesLoaded   bool
	pendingCommits int
	issues         []string
	commits        []string
}

func NewOneOnOne(cfg *config.Config, teamIdx int, jiraClient *jira.Client, memberIdx int) OneOnOneModel {
	team := config.TeamConfig{}
	if teamIdx < len(cfg.Teams) {
		team = cfg.Teams[teamIdx]
	}
	return OneOnOneModel{team: team, jiraClient: jiraClient, memberIdx: memberIdx}
}

func (m OneOnOneModel) loading() bool {
	return !m.issuesLoaded || m.pendingCommits > 0
}

func (m OneOnOneModel) Init() tea.Cmd {
	if m.jiraClient == nil || len(m.team.Members) == 0 {
		return nil
	}
	member := m.team.Members[m.memberIdx]
	since := time.Now().AddDate(0, 0, -14)
	cmds := []tea.Cmd{m.jiraClient.IssuesAssignedTo(member.AccountID)}
	for _, repo := range m.team.Repos {
		cmds = append(cmds, git.FetchCommits(member.AccountID, repo, member.Email, since))
		m.pendingCommits++
	}
	return tea.Batch(cmds...)
}

func (m OneOnOneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return m, func() tea.Msg { return NavigateTo{Screen: ScreenMainMenu} }
		}
	case jira.IssueResult:
		if msg.Err == nil {
			for _, i := range msg.Issues {
				m.issues = append(m.issues, fmt.Sprintf("[%s] %s (%s)", i.Key, i.Summary, i.Status))
			}
		}
		m.issuesLoaded = true
	case git.CommitResult:
		if msg.Err == nil {
			for _, c := range msg.Commits {
				m.commits = append(m.commits, fmt.Sprintf("%s %s (%s, %s)", c.Hash, c.Message, c.Repo, c.Date))
			}
		}
		if m.pendingCommits > 0 {
			m.pendingCommits--
		}
	}
	return m, nil
}

func (m OneOnOneModel) View() string {
	name := "Unknown"
	if m.memberIdx < len(m.team.Members) {
		name = m.team.Members[m.memberIdx].DisplayName
	}
	header := titleStyle.Render("1:1 Prep — "+name) + "\n\n"
	if m.loading() {
		return header + "Loading...\n"
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Open Jira Issues (last 2 weeks)") + "\n")
	if len(m.issues) == 0 {
		sb.WriteString("  none\n")
	}
	for _, i := range m.issues {
		sb.WriteString("  " + i + "\n")
	}
	sb.WriteString("\n" + lipgloss.NewStyle().Bold(true).Render("Recent Commits") + "\n")
	if len(m.commits) == 0 {
		sb.WriteString("  none\n")
	}
	for _, c := range m.commits {
		sb.WriteString("  " + c + "\n")
	}
	sb.WriteString("\n" + lipgloss.NewStyle().Faint(true).Render("esc back"))
	return sb.String()
}
