package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/neilmpatterson/em-tui/internal/config"
)

type menuLevel int

const (
	levelTeamList   menuLevel = iota
	levelTeamDetail           // team actions + member list
	levelPersonDetail
)

// teamDetailItem indices — fixed team-level actions before the member list.
const (
	tdStandup      = 0
	tdSprintReport = 1
	tdModify       = 2
	tdDelete       = 3
	tdMemberOffset = 4 // members start at this index
)

// personDetailItem indices.
const (
	pdOneOnOne   = 0
	pdPerfReview = 1
	pdStats      = 2
	pdDelete     = 3
)

var teamDetailLabels = []string{
	"Standup prep",
	"Sprint report",
	"Modify team",
	"Delete team",
}

var personDetailLabels = []string{
	"1:1 prep",
	"Perf review notes",
	"Stats",
	"Delete member",
}

type MainMenuModel struct {
	cfg          *config.Config
	level        menuLevel
	teamCursor   int
	detailCursor int // position within team detail (team actions + members)
	personCursor int
	activeTeam   int
	activePerson int
	confirmMsg   string // set when awaiting delete confirmation
}

func NewMainMenu(cfg *config.Config) MainMenuModel {
	return MainMenuModel{cfg: cfg}
}

func (m MainMenuModel) Init() tea.Cmd { return nil }

func (m MainMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Any key other than enter clears a pending confirmation.
	if key.String() != "enter" {
		m.confirmMsg = ""
	}

	switch m.level {
	case levelTeamList:
		return m.updateTeamList(key)
	case levelTeamDetail:
		return m.updateTeamDetail(key)
	case levelPersonDetail:
		return m.updatePersonDetail(key)
	}
	return m, nil
}

func (m MainMenuModel) updateTeamList(key tea.KeyMsg) (MainMenuModel, tea.Cmd) {
	n := len(m.cfg.Teams)
	switch key.String() {
	case "up", "k":
		if m.teamCursor > 0 {
			m.teamCursor--
		}
	case "down", "j":
		if m.teamCursor < n-1 {
			m.teamCursor++
		}
	case "enter", " ":
		if n == 0 {
			return m, nil
		}
		m.activeTeam = m.teamCursor
		m.detailCursor = 0
		m.level = levelTeamDetail
	}
	return m, nil
}

func (m MainMenuModel) updateTeamDetail(key tea.KeyMsg) (MainMenuModel, tea.Cmd) {
	team := m.cfg.Teams[m.activeTeam]
	total := tdMemberOffset + len(team.Members)

	switch key.String() {
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < total-1 {
			m.detailCursor++
		}
	case "esc":
		m.level = levelTeamList
	case "enter", " ":
		return m.activateTeamDetail()
	}
	return m, nil
}

func (m MainMenuModel) activateTeamDetail() (MainMenuModel, tea.Cmd) {
	switch m.detailCursor {
	case tdStandup:
		return m, func() tea.Msg {
			return NavigateTo{Screen: ScreenStandup, Payload: m.activeTeam}
		}
	case tdSprintReport:
		return m, func() tea.Msg {
			return NavigateTo{Screen: ScreenSprintReport, Payload: m.activeTeam}
		}
	case tdModify:
		return m, func() tea.Msg { return NavigateTo{Screen: ScreenWizard} }
	case tdDelete:
		if m.confirmMsg == "" {
			m.confirmMsg = "Press enter again to confirm deleting team"
			return m, nil
		}
		idx := m.activeTeam
		m.level = levelTeamList
		m.confirmMsg = ""
		return m, func() tea.Msg { return deleteTeamMsg(idx) }
	default:
		// It's a member row.
		memberIdx := m.detailCursor - tdMemberOffset
		m.activePerson = memberIdx
		m.personCursor = 0
		m.level = levelPersonDetail
	}
	return m, nil
}

func (m MainMenuModel) updatePersonDetail(key tea.KeyMsg) (MainMenuModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.personCursor > 0 {
			m.personCursor--
		}
	case "down", "j":
		if m.personCursor < len(personDetailLabels)-1 {
			m.personCursor++
		}
	case "esc":
		m.level = levelTeamDetail
	case "enter", " ":
		return m.activatePersonDetail()
	}
	return m, nil
}

func (m MainMenuModel) activatePersonDetail() (MainMenuModel, tea.Cmd) {
	p := memberPayload{team: m.activeTeam, member: m.activePerson}
	switch m.personCursor {
	case pdOneOnOne:
		return m, func() tea.Msg { return NavigateTo{Screen: ScreenOneOnOne, Payload: p} }
	case pdPerfReview:
		return m, func() tea.Msg { return NavigateTo{Screen: ScreenPerfReview, Payload: p} }
	case pdStats:
		// stub — no-op for now
	case pdDelete:
		if m.confirmMsg == "" {
			m.confirmMsg = "Press enter again to confirm removing member"
			return m, nil
		}
		t, mb := m.activeTeam, m.activePerson
		m.level = levelTeamDetail
		m.confirmMsg = ""
		return m, func() tea.Msg { return deleteMemberMsg{team: t, member: mb} }
	}
	return m, nil
}

// --- styles ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	normalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dimStyle      = lipgloss.NewStyle().Faint(true)
	separatorStyle = lipgloss.NewStyle().Faint(true)
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func (m MainMenuModel) View() string {
	switch m.level {
	case levelTeamList:
		return m.teamListView()
	case levelTeamDetail:
		return m.teamDetailView()
	case levelPersonDetail:
		return m.personDetailView()
	}
	return ""
}

func (m MainMenuModel) teamListView() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("em-tui") + "\n\n")
	if len(m.cfg.Teams) == 0 {
		sb.WriteString(dimStyle.Render("No teams configured. Run with --setup to add one.") + "\n")
		return sb.String()
	}
	for i, t := range m.cfg.Teams {
		prefix := "  "
		style := normalStyle
		if i == m.teamCursor {
			prefix = "> "
			style = selectedStyle
		}
		summary := dimStyle.Render(fmt.Sprintf("  %d members, %d repos, board %d",
			len(t.Members), len(t.Repos), t.BoardID))
		sb.WriteString(style.Render(prefix+t.Name) + summary + "\n")
	}
	sb.WriteString("\n" + dimStyle.Render("↑/↓ navigate   enter select"))
	return sb.String()
}

func (m MainMenuModel) teamDetailView() string {
	team := m.cfg.Teams[m.activeTeam]
	var sb strings.Builder

	breadcrumb := titleStyle.Render("em-tui") + dimStyle.Render("  /  ") + titleStyle.Render(team.Name)
	meta := dimStyle.Render(fmt.Sprintf("  board %d · %d members · %d repos", team.BoardID, len(team.Members), len(team.Repos)))
	sb.WriteString(breadcrumb + meta + "\n\n")

	for i, label := range teamDetailLabels {
		prefix := "  "
		style := normalStyle
		if i == m.detailCursor {
			prefix = "> "
			style = selectedStyle
		}
		sb.WriteString(style.Render(prefix+label) + "\n")
	}

	sb.WriteString(separatorStyle.Render("  ──────────────") + "\n")

	for i, member := range team.Members {
		itemIdx := tdMemberOffset + i
		prefix := "  "
		style := normalStyle
		if itemIdx == m.detailCursor {
			prefix = "> "
			style = selectedStyle
		}
		sb.WriteString(style.Render(prefix+member.DisplayName) + "\n")
	}

	if m.confirmMsg != "" {
		sb.WriteString("\n" + warnStyle.Render("  "+m.confirmMsg) + "\n")
	}
	sb.WriteString("\n" + dimStyle.Render("↑/↓ navigate   enter select   esc back"))
	return sb.String()
}

func (m MainMenuModel) personDetailView() string {
	team := m.cfg.Teams[m.activeTeam]
	member := team.Members[m.activePerson]

	var sb strings.Builder
	breadcrumb := titleStyle.Render("em-tui") +
		dimStyle.Render("  /  ") + titleStyle.Render(team.Name) +
		dimStyle.Render("  /  ") + titleStyle.Render(member.DisplayName)
	sb.WriteString(breadcrumb + "\n\n")

	for i, label := range personDetailLabels {
		prefix := "  "
		style := normalStyle
		if i == m.personCursor {
			prefix = "> "
			style = selectedStyle
		}
		sb.WriteString(style.Render(prefix+label) + "\n")
	}

	if m.confirmMsg != "" {
		sb.WriteString("\n" + warnStyle.Render("  "+m.confirmMsg) + "\n")
	}
	sb.WriteString("\n" + dimStyle.Render("↑/↓ navigate   enter select   esc back"))
	return sb.String()
}
