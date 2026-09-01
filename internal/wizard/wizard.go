package wizard

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

// DoneMsg is emitted when setup is complete.
type DoneMsg struct {
	Config     *config.Config
	ConfigPath string
}

type step int

const (
	stepJiraURL      step = iota
	stepEmail             // enter Jira email
	stepToken             // enter API token
	stepTeamOverview      // list existing teams, add/edit/done
	stepTeamEdit          // within an existing team: add members, add repos, done
	stepTeamName          // name a new team
	stepSearchMembers     // search Jira for members
	stepSelectMembers     // pick from search results
	stepAddRepo           // add repos one at a time
	stepBoardID           // board ID for the team
)

// editMenuItem is an option shown in stepTeamEdit.
type editMenuItem int

const (
	editAddMembers editMenuItem = iota
	editAddRepos
	editDone
)

// Model is the setup wizard.
type Model struct {
	step       step
	input      string
	cfg        config.Config
	jiraClient *jira.Client

	// Team overview
	overviewCursor int

	// Per-team editing
	editingIdx  int                // index in cfg.Teams; -1 = new team
	teamDraft   config.TeamConfig  // team being built/edited
	editCursor  int                // cursor in stepTeamEdit menu

	// Member search
	candidates   []domain.TeamMember
	memberCursor int
	selected     map[int]bool
	searching    bool

	err string
}

// New creates the wizard, seeding credentials from env vars and loading
// any existing config so that --setup doesn't wipe what's already there.
func New(existingCfg *config.Config) Model {
	m := Model{
		selected:   make(map[int]bool),
		editingIdx: -1,
	}

	if existingCfg != nil {
		m.cfg = *existingCfg
	}

	// Overlay env vars (higher priority than stored config).
	if v := os.Getenv("JIRA_URL"); v != "" {
		m.cfg.Jira.BaseURL = v
	}
	if v := os.Getenv("JIRA_EMAIL"); v != "" {
		m.cfg.Jira.Email = v
	}
	if v := os.Getenv("JIRA_API_TOKEN"); v != "" {
		m.cfg.Jira.APIToken = v
	}

	m.step = m.firstStep()

	// If all creds are available, connect now (jira.New is sync, no network call).
	if m.step == stepTeamOverview {
		if c, err := jira.New(m.cfg.Jira.BaseURL, m.cfg.Jira.Email, m.cfg.Jira.APIToken); err == nil {
			m.jiraClient = c
		}
	}

	return m
}

func (m Model) firstStep() step {
	if m.cfg.Jira.BaseURL == "" {
		return stepJiraURL
	}
	if m.cfg.Jira.Email == "" {
		return stepEmail
	}
	if m.cfg.Jira.APIToken == "" {
		return stepToken
	}
	return stepTeamOverview
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case jira.UserSearchResult:
		m.searching = false
		if msg.Err != nil {
			m.err = "Jira search failed: " + msg.Err.Error()
		} else {
			m.candidates = msg.Users
			m.err = ""
		}
		return m, nil

	case tea.KeyMsg:
		m.err = ""
		switch m.step {
		case stepTeamOverview:
			return m.updateOverview(msg)
		case stepTeamEdit:
			return m.updateTeamEdit(msg)
		case stepSearchMembers:
			return m.updateSearchMembers(msg)
		case stepSelectMembers:
			return m.updateSelectMembers(msg)
		case stepAddRepo:
			return m.updateAddRepo(msg)
		default:
			return m.updateTextStep(msg)
		}
	}
	return m, nil
}

// --- per-step Update handlers ---

func (m Model) updateOverview(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Items: 0..len(teams)-1 are teams, then "Add new team", then "Done"
	total := len(m.cfg.Teams) + 2
	addIdx := len(m.cfg.Teams)
	doneIdx := len(m.cfg.Teams) + 1

	switch msg.String() {
	case "up", "k":
		if m.overviewCursor > 0 {
			m.overviewCursor--
		}
	case "down", "j":
		if m.overviewCursor < total-1 {
			m.overviewCursor++
		}
	case "enter", " ":
		switch m.overviewCursor {
		case addIdx:
			// Start a new team
			m.editingIdx = -1
			m.teamDraft = config.TeamConfig{}
			m.step = stepTeamName
		case doneIdx:
			if len(m.cfg.Teams) == 0 {
				m.err = "Add at least one team before finishing."
				return m, nil
			}
			return m.finish()
		default:
			// Edit an existing team
			m.editingIdx = m.overviewCursor
			m.teamDraft = m.cfg.Teams[m.overviewCursor]
			m.editCursor = 0
			m.step = stepTeamEdit
		}
	}
	return m, nil
}

func (m Model) updateTeamEdit(msg tea.KeyMsg) (Model, tea.Cmd) {
	items := 3 // Add members / Add repos / Done
	switch msg.String() {
	case "up", "k":
		if m.editCursor > 0 {
			m.editCursor--
		}
	case "down", "j":
		if m.editCursor < items-1 {
			m.editCursor++
		}
	case "enter", " ":
		switch editMenuItem(m.editCursor) {
		case editAddMembers:
			m.step = stepSearchMembers
		case editAddRepos:
			m.step = stepAddRepo
		case editDone:
			m = m.saveTeamDraft()
			m.step = stepTeamOverview
		}
	case "esc":
		m.step = stepTeamOverview
	}
	return m, nil
}

func (m Model) updateSearchMembers(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.jiraClient == nil {
			m.err = "Jira client not ready"
			return m, nil
		}
		if strings.TrimSpace(m.input) == "" {
			// blank = done searching, back to wherever we came from
			return m.doneAddingMembers()
		}
		query := m.input
		m.input = ""
		m.searching = true
		m.step = stepSelectMembers
		return m, m.jiraClient.SearchUsers(query)
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "esc":
		return m.doneAddingMembers()
	default:
		if len(msg.Runes) > 0 {
			m.input += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) doneAddingMembers() (Model, tea.Cmd) {
	if m.editingIdx >= 0 {
		m.step = stepTeamEdit
	} else {
		m.step = stepAddRepo
	}
	return m, nil
}

func (m Model) updateSelectMembers(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.memberCursor > 0 {
			m.memberCursor--
		}
	case "down", "j":
		if m.memberCursor < len(m.candidates)-1 {
			m.memberCursor++
		}
	case " ":
		if m.selected[m.memberCursor] {
			delete(m.selected, m.memberCursor)
		} else {
			m.selected[m.memberCursor] = true
		}
	case "enter":
		for idx := range m.selected {
			if idx < len(m.candidates) {
				m.teamDraft.Members = append(m.teamDraft.Members, m.candidates[idx])
			}
		}
		m.candidates = nil
		m.selected = make(map[int]bool)
		m.memberCursor = 0
		m.step = stepSearchMembers // loop back to search for more
	case "esc":
		m.step = stepSearchMembers
		m.candidates = nil
		m.selected = make(map[int]bool)
		m.memberCursor = 0
	}
	return m, nil
}

func (m Model) updateAddRepo(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := strings.TrimSpace(m.input)
		if val == "" {
			// done adding repos
			return m.doneAddingRepos()
		}
		m.teamDraft.Repos = append(m.teamDraft.Repos, val)
		m.input = ""
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "esc":
		return m.doneAddingRepos()
	default:
		if len(msg.Runes) > 0 {
			m.input += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) doneAddingRepos() (Model, tea.Cmd) {
	if m.editingIdx >= 0 {
		m.step = stepTeamEdit
	} else {
		m.step = stepBoardID
	}
	return m, nil
}

func (m Model) updateTextStep(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.advance()
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			m.input += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) advance() (Model, tea.Cmd) {
	val := strings.TrimSpace(m.input)
	if val == "" {
		m.err = "Required"
		return m, nil
	}
	m.input = ""
	switch m.step {
	case stepJiraURL:
		m.cfg.Jira.BaseURL = val
		m.step = stepEmail
	case stepEmail:
		m.cfg.Jira.Email = val
		if m.cfg.Jira.APIToken != "" {
			return m.connectJira()
		}
		m.step = stepToken
	case stepToken:
		m.cfg.Jira.APIToken = val
		return m.connectJira()
	case stepTeamName:
		m.teamDraft.Name = val
		m.step = stepSearchMembers
	case stepBoardID:
		var id int
		fmt.Sscanf(val, "%d", &id)
		m.teamDraft.BoardID = id
		m = m.saveTeamDraft()
		m.step = stepTeamOverview
	}
	return m, nil
}

func (m Model) connectJira() (Model, tea.Cmd) {
	c, err := jira.New(m.cfg.Jira.BaseURL, m.cfg.Jira.Email, m.cfg.Jira.APIToken)
	if err != nil {
		m.err = "Could not connect to Jira: " + err.Error()
		return m, nil
	}
	m.jiraClient = c
	m.step = stepTeamOverview
	return m, nil
}

// saveTeamDraft writes teamDraft back into cfg.Teams.
func (m Model) saveTeamDraft() Model {
	if m.editingIdx == -1 {
		m.cfg.Teams = append(m.cfg.Teams, m.teamDraft)
		m.overviewCursor = len(m.cfg.Teams) - 1
	} else {
		m.cfg.Teams[m.editingIdx] = m.teamDraft
	}
	m.editingIdx = -1
	m.teamDraft = config.TeamConfig{}
	return m
}

func (m Model) finish() (Model, tea.Cmd) {
	cfgPath := config.DefaultPath()
	cfg := m.cfg
	_ = config.Save(&cfg, cfgPath)
	return m, func() tea.Msg {
		return DoneMsg{Config: &cfg, ConfigPath: cfgPath}
	}
}

// --- View ---

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	promptStyle  = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func (m Model) View() string {
	title := titleStyle.Render("em-tui Setup") + "\n\n"

	switch m.step {
	case stepJiraURL:
		return title + m.textPrompt("Jira base URL (e.g. https://yourco.atlassian.net):\n  " +
			dimStyle.Render("tip: set JIRA_URL to skip this"))
	case stepEmail:
		pre := ""
		if m.cfg.Jira.BaseURL != "" {
			pre = dimStyle.Render("URL: "+m.cfg.Jira.BaseURL) + "\n"
		}
		return title + pre + m.textPrompt("Jira account email:\n  "+
			dimStyle.Render("tip: set JIRA_EMAIL to skip this"))
	case stepToken:
		return title + m.textPrompt("Jira API token:\n  " +
			dimStyle.Render("JIRA_API_TOKEN not set — enter token, or ctrl+c and export it"))
	case stepTeamOverview:
		return title + m.overviewView()
	case stepTeamEdit:
		return title + m.teamEditView()
	case stepTeamName:
		return title + m.textPrompt("Team name (e.g. Platform, Frontend):")
	case stepSearchMembers:
		return title + m.searchMembersView()
	case stepSelectMembers:
		return title + m.selectMembersView()
	case stepAddRepo:
		return title + m.addRepoView()
	case stepBoardID:
		return title + m.textPrompt(fmt.Sprintf("Jira board ID for %q (number in board URL):", m.teamDraft.Name))
	default:
		return title + "Setup complete!\n"
	}
}

func (m Model) overviewView() string {
	var sb strings.Builder
	sb.WriteString(promptStyle.Render("Teams") + "\n\n")

	addIdx := len(m.cfg.Teams)
	doneIdx := len(m.cfg.Teams) + 1

	for i, t := range m.cfg.Teams {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.overviewCursor {
			prefix = "> "
			style = selectedStyle
		}
		summary := fmt.Sprintf("%s  %s", t.Name,
			dimStyle.Render(fmt.Sprintf("(%d members, %d repos, board %d)",
				len(t.Members), len(t.Repos), t.BoardID)))
		sb.WriteString(style.Render(prefix) + summary + "\n")
	}

	// "Add new team" option
	addStyle := lipgloss.NewStyle()
	addPrefix := "  "
	if m.overviewCursor == addIdx {
		addStyle = selectedStyle
		addPrefix = "> "
	}
	sb.WriteString(addStyle.Render(addPrefix+"Add new team") + "\n")

	// "Done" option
	doneStyle := lipgloss.NewStyle()
	donePrefix := "  "
	if m.overviewCursor == doneIdx {
		doneStyle = selectedStyle
		donePrefix = "> "
	}
	sb.WriteString(doneStyle.Render(donePrefix+"Done") + "\n")

	if m.err != "" {
		sb.WriteString("\n" + errStyle.Render(m.err) + "\n")
	}
	sb.WriteString("\n" + dimStyle.Render("↑/↓ navigate   enter select"))
	return sb.String()
}

func (m Model) teamEditView() string {
	var sb strings.Builder
	sb.WriteString(promptStyle.Render("Editing: "+m.teamDraft.Name) + "\n")
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%d members, %d repos, board %d",
		len(m.teamDraft.Members), len(m.teamDraft.Repos), m.teamDraft.BoardID)) + "\n\n")

	items := []string{"Add members", "Add repos", "Done editing"}
	for i, label := range items {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.editCursor {
			prefix = "> "
			style = selectedStyle
		}
		sb.WriteString(style.Render(prefix+label) + "\n")
	}
	sb.WriteString("\n" + dimStyle.Render("↑/↓ navigate   enter select   esc back"))
	return sb.String()
}

func (m Model) searchMembersView() string {
	hint := "Search for a member by name or email"
	if len(m.teamDraft.Members) > 0 {
		names := make([]string, len(m.teamDraft.Members))
		for i, t := range m.teamDraft.Members {
			names[i] = t.DisplayName
		}
		hint = fmt.Sprintf("Members (%d): %s\nSearch for another, or press enter with blank input when done:",
			len(m.teamDraft.Members), strings.Join(names, ", "))
	} else {
		hint += " (enter blank to skip):"
	}
	return m.textPrompt(hint)
}

func (m Model) selectMembersView() string {
	if m.searching {
		return "Searching Jira...\n"
	}
	var sb strings.Builder
	sb.WriteString(promptStyle.Render(fmt.Sprintf("Select members (%d results):", len(m.candidates))) + "\n\n")
	for i, c := range m.candidates {
		prefix := "  [ ] "
		if m.selected[i] {
			prefix = "  [x] "
		}
		line := prefix + c.DisplayName + " <" + c.Email + ">"
		if i == m.memberCursor {
			sb.WriteString(selectedStyle.Render(line) + "\n")
		} else {
			sb.WriteString(line + "\n")
		}
	}
	if m.err != "" {
		sb.WriteString("\n" + errStyle.Render(m.err) + "\n")
	}
	sb.WriteString("\n" + dimStyle.Render("↑/↓ navigate   space toggle   enter confirm   esc back"))
	return sb.String()
}

func (m Model) addRepoView() string {
	hint := "Path to a git repository"
	if len(m.teamDraft.Repos) > 0 {
		hint = fmt.Sprintf("Repos (%d): %s\nAdd another, or press enter with blank input when done:",
			len(m.teamDraft.Repos), strings.Join(m.teamDraft.Repos, ", "))
	} else {
		hint += " (enter blank to skip):"
	}
	return m.textPrompt(hint)
}

func (m Model) textPrompt(label string) string {
	out := promptStyle.Render(label) + "\n"
	out += "> " + m.input + "_\n"
	if m.err != "" {
		out += errStyle.Render(m.err) + "\n"
	}
	out += "\n" + dimStyle.Render("enter confirm   ctrl+c quit")
	return out
}
