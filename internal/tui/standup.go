package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

type standupView int

const (
	viewSectionMenu standupView = iota
	viewIssueTable
)

// sectionSecurityNew is an internal routing key for date-gated security results
// that get merged into SectionBugs. Not shown in the menu.
const sectionSecurityNew = "securitynew"

var sectionTitles = map[string]string{
	jira.SectionBugs:       "New Bugs & Security",
	jira.SectionSecurity:   "Security Issues",
	jira.SectionTriage:     "Needs Triage",
	jira.SectionInProgress: "In Progress",
	jira.SectionDone:       "Completed",
	jira.SectionStuck:      "Stuck (2+ days)",
	jira.SectionMidSprint:  "Scope-Creep Tickets",
}

var workflowOrder = []string{
	"To Do", "Open", "Backlog",
	"In Progress",
	"Blocked",
	"Code Review", "In Code Review",
	"Ready for QA", "QA Ready",
	"Testing", "QA Testing", "In Testing",
	"Ready to Merge",
	"Done", "Closed", "Resolved", "Released",
}

type StandupModel struct {
	team       config.TeamConfig
	jiraClient *jira.Client

	sprint         domain.Sprint
	sprintDone     bool
	noActiveSprint bool

	allSprintIssues []domain.JiraIssue
	sprintLoaded    bool

	activeSections []string
	issues         map[string][]domain.JiraIssue
	sectionErrs    map[string]string
	// pending tracks how many in-flight fetches remain per section (0 = done).
	pending map[string]int

	burndownData  map[string]int // date → remaining issues count
	burndownTotal int

	view          standupView
	cursor        int
	activeSection string

	viewport viewport.Model
	ready    bool
	width    int
	height   int
}

func NewStandup(cfg *config.Config, teamIdx int, jiraClient *jira.Client, width, height int) StandupModel {
	team := config.TeamConfig{}
	if teamIdx < len(cfg.Teams) {
		team = cfg.Teams[teamIdx]
	}
	isKanban := strings.ToLower(team.BoardType) == "kanban"

	var active []string
	pending := make(map[string]int)

	// Bugs: one fetch for incoming_bugs + one for new security (if configured).
	active = append(active, jira.SectionBugs)
	pending[jira.SectionBugs]++

	if team.SecurityIssues != "" {
		active = append(active, jira.SectionSecurity)
		pending[jira.SectionSecurity]++
		pending[jira.SectionBugs]++ // second fetch: new security merged in
	}

	if len(team.Projects) > 0 {
		active = append(active, jira.SectionTriage)
		pending[jira.SectionTriage]++
	}

	if !isKanban && team.BoardID != 0 {
		active = append(active,
			jira.SectionInProgress,
			jira.SectionDone,
			jira.SectionStuck,
			jira.SectionMidSprint,
		)
		pending[jira.SectionInProgress]++
		pending[jira.SectionDone]++
		pending[jira.SectionStuck]++
		pending[jira.SectionMidSprint]++
		pending[jira.SectionWatch]++
	}

	const headerLines, footerLines = 2, 3
	vpHeight := height - headerLines - footerLines
	if vpHeight < 5 {
		vpHeight = 5
	}
	vp := viewport.New(width, vpHeight)

	return StandupModel{
		team:           team,
		jiraClient:     jiraClient,
		activeSections: active,
		issues:         make(map[string][]domain.JiraIssue),
		sectionErrs:    make(map[string]string),
		pending:        pending,
		viewport:       vp,
		ready:          width > 0 && height > 0,
		width:          width,
		height:         height,
	}
}

func (m StandupModel) Init() tea.Cmd {
	if m.jiraClient == nil {
		return nil
	}
	isKanban := strings.ToLower(m.team.BoardType) == "kanban"
	sinceStr := sinceLastBusinessDay().Format("2006-01-02")

	var cmds []tea.Cmd

	// Incoming bugs (date-gated).
	cmds = append(cmds, resolveFilterOrJQL(jira.SectionBugs, m.team.IncomingBugs, sinceStr, m.team, m.jiraClient))

	if m.team.SecurityIssues != "" {
		// All open security issues — no date gate.
		cmds = append(cmds, resolveAllOpenJQL(jira.SectionSecurity, m.team.SecurityIssues, m.jiraClient))
		// New security bugs (date-gated) merged into SectionBugs.
		cmds = append(cmds, resolveFilterOrJQL(sectionSecurityNew, m.team.SecurityIssues, sinceStr, m.team, m.jiraClient))
	}

	if len(m.team.Projects) > 0 {
		projClause := buildProjectInClause(m.team.Projects)
		tJQL := fmt.Sprintf(`issuetype = Bug AND project in (%s) AND status = "Needs Triage" ORDER BY created ASC`, projClause)
		cmds = append(cmds, m.jiraClient.FetchAllIssuesByJQL(jira.SectionTriage, tJQL))
	}

	if !isKanban && m.team.BoardID != 0 {
		cmds = append(cmds, m.jiraClient.FetchActiveSprint(m.team.BoardID))
	} else {
		statuses := m.team.EffectiveWatchStatuses()
		wjql := fmt.Sprintf(`status in (%s) ORDER BY updated ASC`, buildStatusInClause(statuses))
		cmds = append(cmds, m.jiraClient.FetchSprintIssues(jira.SectionWatch, wjql, 50))
	}

	return tea.Batch(cmds...)
}

func (m StandupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		const headerLines, footerLines = 2, 3
		vpHeight := msg.Height - headerLines - footerLines
		if vpHeight < 5 {
			vpHeight = 5
		}
		if !m.ready {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = vpHeight
		}
		m.viewport.SetContent(m.currentContent())
		return m, nil

	case jira.SprintResult:
		if msg.Err != nil {
			if errors.Is(msg.Err, jira.ErrNoActiveSprint) {
				m.noActiveSprint = true
			}
			for _, s := range []string{jira.SectionInProgress, jira.SectionDone,
				jira.SectionWatch, jira.SectionStuck, jira.SectionMidSprint} {
				m.pending[s] = 0
			}
			m.viewport.SetContent(m.currentContent())
			return m, nil
		}
		m.sprint = msg.Sprint
		m.sprintDone = true
		m.viewport.SetContent(m.currentContent())
		return m, m.jiraClient.FetchBoardSprintIssues(m.team.BoardID, msg.Sprint.ID)

	case jira.AllSprintIssuesResult:
		if msg.Err != nil {
			for _, s := range []string{jira.SectionInProgress, jira.SectionDone,
				jira.SectionWatch, jira.SectionStuck, jira.SectionMidSprint} {
				if m.pending[s] > 0 {
					m.sectionErrs[s] = msg.Err.Error()
				}
				m.pending[s] = 0
			}
			m.viewport.SetContent(m.currentContent())
			return m, nil
		}
		m.allSprintIssues = msg.Issues
		m.sprintLoaded = true
		m.deriveSprintSections()
		m.viewport.SetContent(m.currentContent())
		return m, nil

	case jira.FilterResult:
		if msg.Err != nil {
			target := bugsTargetSection(msg.Section)
			m.sectionErrs[target] = msg.Err.Error()
			m.pending[target]--
			m.viewport.SetContent(m.currentContent())
			return m, nil
		}
		sinceStr := sinceLastBusinessDay().Format("2006-01-02")
		base := stripOrderBy(msg.JQL)
		var cmd tea.Cmd
		switch msg.Section {
		case jira.SectionSecurity:
			// No date gate, paginate to get all open security issues.
			cmd = m.jiraClient.FetchAllIssuesByJQL(jira.SectionSecurity, base)
		case sectionSecurityNew:
			jql := fmt.Sprintf(`(%s) AND created >= "%s"`, base, sinceStr)
			cmd = m.jiraClient.FetchSprintIssues(sectionSecurityNew, jql, 20)
		default:
			jql := fmt.Sprintf(`(%s) AND created >= "%s"`, base, sinceStr)
			cmd = m.jiraClient.FetchSprintIssues(msg.Section, jql, 20)
		}
		return m, cmd

	case jira.SprintIssuesResult:
		if msg.Err != nil {
			if msg.Section == sectionSecurityNew {
				m.pending[jira.SectionBugs]--
			} else {
				m.sectionErrs[msg.Section] = msg.Err.Error()
				m.pending[msg.Section]--
			}
			m.viewport.SetContent(m.currentContent())
			return m, nil
		}
		if msg.Section == sectionSecurityNew {
			m.issues[jira.SectionBugs] = append(m.issues[jira.SectionBugs], msg.Issues...)
			m.pending[jira.SectionBugs]--
		} else {
			m.issues[msg.Section] = msg.Issues
			m.pending[msg.Section]--
		}
		m.viewport.SetContent(m.currentContent())
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// bugsTargetSection maps sectionSecurityNew → SectionBugs for error routing.
func bugsTargetSection(section string) string {
	if section == sectionSecurityNew {
		return jira.SectionBugs
	}
	return section
}

func (m *StandupModel) deriveSprintSections() {
	twoDaysAgo := time.Now().AddDate(0, 0, -2)
	var inProgress, done, stuck, midSprint []domain.JiraIssue

	for _, iss := range m.allSprintIssues {
		cat := strings.ToLower(iss.StatusCategory)

		switch {
		case cat == "done":
			done = append(done, iss)
		case cat == "in progress":
			inProgress = append(inProgress, iss)
			// Stuck = no comment, status change, or assignee change in 2+ days.
			// Jira's Updated field advances on any of those events, so stale Updated
			// means genuinely idle.
			if t, err := time.Parse("2006-01-02", iss.Updated); err == nil && t.Before(twoDaysAgo) {
				stuck = append(stuck, iss)
			}
		}
		if m.sprint.StartDate != "" && iss.Created >= m.sprint.StartDate {
			midSprint = append(midSprint, iss)
		}
	}

	m.issues[jira.SectionInProgress] = inProgress
	m.issues[jira.SectionDone] = done
	m.issues[jira.SectionStuck] = stuck
	m.issues[jira.SectionMidSprint] = midSprint
	m.issues[jira.SectionWatch] = m.allSprintIssues

	m.burndownTotal = len(m.allSprintIssues)
	m.burndownData = computeBurndown(m.allSprintIssues, m.sprint)

	for _, s := range []string{jira.SectionInProgress, jira.SectionDone,
		jira.SectionWatch, jira.SectionStuck, jira.SectionMidSprint} {
		m.pending[s] = 0
	}
}

func computeBurndown(issues []domain.JiraIssue, sprint domain.Sprint) map[string]int {
	if sprint.StartDate == "" || sprint.EndDate == "" {
		return nil
	}
	start, err := time.Parse("2006-01-02", sprint.StartDate)
	if err != nil {
		return nil
	}
	end, err := time.Parse("2006-01-02", sprint.EndDate)
	if err != nil {
		return nil
	}

	total := len(issues)
	today := time.Now().Truncate(24 * time.Hour)
	if today.After(end) {
		today = end
	}

	dailyCompleted := make(map[string]int)
	for _, iss := range issues {
		if strings.ToLower(iss.StatusCategory) == "done" {
			dailyCompleted[iss.Updated]++
		}
	}

	remaining := total
	result := make(map[string]int)
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		remaining -= dailyCompleted[dateStr]
		if remaining < 0 {
			remaining = 0
		}
		result[dateStr] = remaining
	}
	return result
}

func (m StandupModel) handleKey(msg tea.KeyMsg) (StandupModel, tea.Cmd) {
	switch m.view {
	case viewSectionMenu:
		return m.handleMenuKey(msg)
	case viewIssueTable:
		return m.handleTableKey(msg)
	}
	return m, nil
}

func (m StandupModel) handleMenuKey(msg tea.KeyMsg) (StandupModel, tea.Cmd) {
	n := len(m.activeSections)
	switch msg.String() {
	case "esc", "q":
		return m, func() tea.Msg { return GoBack{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.viewport.SetContent(m.renderSectionMenu())
		}
	case "down", "j":
		if m.cursor < n-1 {
			m.cursor++
			m.viewport.SetContent(m.renderSectionMenu())
		}
	case "enter", " ":
		if n == 0 {
			break
		}
		m.activeSection = m.activeSections[m.cursor]
		m.view = viewIssueTable
		m.viewport.GotoTop()
		m.viewport.SetContent(m.renderIssueTable())
	}
	return m, nil
}

func (m StandupModel) handleTableKey(msg tea.KeyMsg) (StandupModel, tea.Cmd) {
	if msg.String() == "esc" || msg.String() == "q" {
		m.view = viewSectionMenu
		m.viewport.GotoTop()
		m.viewport.SetContent(m.renderSectionMenu())
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m StandupModel) currentContent() string {
	if m.view == viewIssueTable {
		return m.renderIssueTable()
	}
	return m.renderSectionMenu()
}

func (m StandupModel) View() string {
	header := m.headerView() + "\n"
	var footer string
	switch m.view {
	case viewSectionMenu:
		footer = "\n" + dimStyle.Render("j/k navigate  enter open  esc back")
	case viewIssueTable:
		footer = "\n" + dimStyle.Render("j/k scroll  esc back")
	}
	if !m.ready {
		return header + dimStyle.Render("  Initializing...") + footer
	}
	return header + m.viewport.View() + footer
}

func (m StandupModel) headerView() string {
	isKanban := strings.ToLower(m.team.BoardType) == "kanban"
	title := titleStyle.Render("Standup Prep") + dimStyle.Render("  —  ") + titleStyle.Render(m.team.Name)
	if isKanban {
		return title + dimStyle.Render("  (Kanban)")
	}
	if m.noActiveSprint {
		return title + dimStyle.Render("  No active sprint")
	}
	if m.sprintDone {
		return title + dimStyle.Render("  "+m.sprint.Name+"  "+sprintDayInfo(m.sprint))
	}
	return title + dimStyle.Render("  (loading sprint...)")
}

// --- Section menu ---

func (m StandupModel) renderSectionMenu() string {
	var sb strings.Builder

	// Status table + burndown side by side (if wide enough and burndown available).
	statusPanel := m.renderStatusTablePlain()
	if m.width >= 90 && m.burndownData != nil {
		burndownPanel := m.renderBurndownInline()
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, statusPanel, "    ", burndownPanel))
	} else {
		sb.WriteString(statusPanel)
	}
	sb.WriteString("\n")

	for i, key := range m.activeSections {
		sb.WriteString(m.renderSectionMenuItem(key, i))
	}
	return sb.String()
}

func (m StandupModel) renderStatusTablePlain() string {
	loading := m.pending[jira.SectionWatch] > 0
	errMsg := m.sectionErrs[jira.SectionWatch]

	if errMsg != "" {
		return warnStyle.Render("  ⚠ "+errMsg) + "\n"
	}
	if loading {
		return dimStyle.Render("  (loading...)") + "\n"
	}

	counts := make(map[string]int)
	for _, iss := range m.issues[jira.SectionWatch] {
		counts[iss.Status]++
	}
	if len(counts) == 0 {
		return dimStyle.Render("  (none)") + "\n"
	}

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"STATUS", "COUNT"})
	for _, s := range sortByWorkflow(counts) {
		t.AppendRow(table.Row{s, counts[s]})
	}

	var sb strings.Builder
	for _, line := range strings.Split(t.Render(), "\n") {
		if line != "" {
			sb.WriteString("  " + line + "\n")
		}
	}
	return sb.String()
}

// renderBurndownInline renders a compact burndown chart for side-by-side display.
func (m StandupModel) renderBurndownInline() string {
	if m.burndownData == nil || m.burndownTotal == 0 {
		return ""
	}
	start, err := time.Parse("2006-01-02", m.sprint.StartDate)
	if err != nil {
		return ""
	}
	end, err := time.Parse("2006-01-02", m.sprint.EndDate)
	if err != nil {
		return ""
	}

	total := m.burndownTotal
	chartW := int(end.Sub(start).Hours()/24) + 1
	if chartW > 20 {
		chartW = 20
	}
	chartH := 8

	today := time.Now().Truncate(24 * time.Hour)
	todayCol := int(today.Sub(start).Hours() / 24)

	yLabelW := len(strconv.Itoa(total)) + 1

	type cellKind uint8
	const (
		cellEmpty  cellKind = 0
		cellIdeal  cellKind = 1
		cellActual cellKind = 2
	)
	grid := make([][]cellKind, chartH)
	for r := range grid {
		grid[r] = make([]cellKind, chartW)
	}

	// Ideal line.
	for x := 0; x < chartW; x++ {
		idealRemaining := total - (total*x)/(chartW-1)
		r := chartH - 1 - (idealRemaining*(chartH-1))/total
		r = clamp(r, 0, chartH-1)
		if grid[r][x] == cellEmpty {
			grid[r][x] = cellIdeal
		}
	}

	// Actual data points.
	for x := 0; x < chartW; x++ {
		d := start.AddDate(0, 0, x)
		remaining, ok := m.burndownData[d.Format("2006-01-02")]
		if !ok {
			continue
		}
		r := chartH - 1 - (remaining*(chartH-1))/total
		r = clamp(r, 0, chartH-1)
		grid[r][x] = cellActual
	}

	// Header matching the status table style.
	chartTotalW := yLabelW + 2 + chartW // label + " ┤" + chart columns
	sep := strings.Repeat("─", chartTotalW)

	var sb strings.Builder
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%-*s", chartTotalW, "BURNDOWN")) + "\n")
	sb.WriteString(sep + "\n")

	for r := 0; r < chartH; r++ {
		issueCount := total * (chartH - 1 - r) / (chartH - 1)
		sb.WriteString(fmt.Sprintf("%*d ┤", yLabelW, issueCount))
		for x := 0; x < chartW; x++ {
			switch grid[r][x] {
			case cellActual:
				sb.WriteString(selectedStyle.Render("●"))
			case cellIdeal:
				sb.WriteString(dimStyle.Render("·"))
			default:
				if x == todayCol {
					sb.WriteString(dimStyle.Render("│"))
				} else {
					sb.WriteString(" ")
				}
			}
		}
		sb.WriteString("\n")
	}

	// X-axis.
	sb.WriteString(fmt.Sprintf("%*s └%s\n", yLabelW, "", strings.Repeat("─", chartW)))

	// Today marker + day labels.
	if todayCol >= 0 && todayCol < chartW {
		sb.WriteString(fmt.Sprintf("%*s  %s▲\n", yLabelW, "", strings.Repeat(" ", todayCol)))
	}
	lastLabel := fmt.Sprintf("D%d", chartW)
	padding := chartW - 2 - len(lastLabel)
	if padding < 0 {
		padding = 0
	}
	sb.WriteString(fmt.Sprintf("%*s  D1%s%s\n", yLabelW, "", strings.Repeat(" ", padding), lastLabel))

	// Minimal legend.
	sb.WriteString(dimStyle.Render(fmt.Sprintf("%*s  · ideal  ", yLabelW, "")) +
		selectedStyle.Render("●") + dimStyle.Render(" actual\n"))

	return sb.String()
}

func (m StandupModel) renderSectionMenuItem(key string, idx int) string {
	var badge string
	switch {
	case m.sectionErrs[key] != "":
		badge = "[!]"
	case m.pending[key] > 0:
		badge = "[?]"
	default:
		badge = fmt.Sprintf("[%d]", len(m.issues[key]))
	}

	prefix := "  "
	s := normalStyle
	if idx == m.cursor {
		prefix = "> "
		s = selectedStyle
	}
	line := fmt.Sprintf("%s%-32s %s", prefix, sectionTitles[key], badge)
	return s.Render(line) + "\n"
}

// --- Issue table ---

func (m StandupModel) renderIssueTable() string {
	section := m.activeSection
	loading := m.pending[section] > 0
	errMsg := m.sectionErrs[section]
	issues := m.issues[section]

	countStr := "[?]"
	if !loading {
		countStr = fmt.Sprintf("[%d]", len(issues))
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("  "+sectionTitles[section]) + "  " + dimStyle.Render(countStr) + "\n\n")

	if loading {
		sb.WriteString(dimStyle.Render("  (loading...)") + "\n")
		return sb.String()
	}
	if errMsg != "" {
		sb.WriteString(warnStyle.Render("  ⚠ "+errMsg) + "\n")
		return sb.String()
	}
	if len(issues) == 0 {
		sb.WriteString(dimStyle.Render("  (none)") + "\n")
		return sb.String()
	}
	sb.WriteString(m.renderIssueTableRows(issues))
	return sb.String()
}

func (m StandupModel) renderIssueTableRows(issues []domain.JiraIssue) string {
	w := m.width
	if w < 80 {
		w = 100
	}
	// Fixed: key(11)+pri(6)+status(16)+assignee(20) + borders/gaps ~22
	summaryW := w - 11 - 6 - 16 - 20 - 22
	if summaryW < 20 {
		summaryW = 20
	}

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, WidthMax: 11},
		{Number: 2, WidthMax: 6},
		{Number: 3, WidthMax: 16},
		{Number: 4, WidthMax: 20},
		{Number: 5, WidthMax: summaryW},
	})
	t.AppendHeader(table.Row{"KEY", "PRI", "STATUS", "ASSIGNEE", "SUMMARY"})
	for _, iss := range issues {
		t.AppendRow(table.Row{
			iss.Key,
			priorityAbbr(iss.Priority),
			iss.Status,
			iss.Assignee,
			iss.Summary,
		})
	}

	var sb strings.Builder
	for _, line := range strings.Split(t.Render(), "\n") {
		if line != "" {
			sb.WriteString("  " + line + "\n")
		}
	}
	return sb.String()
}

// --- Helpers ---

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func sortByWorkflow(counts map[string]int) []string {
	rank := make(map[string]int, len(workflowOrder))
	for i, s := range workflowOrder {
		rank[s] = i
	}
	statuses := make([]string, 0, len(counts))
	for s := range counts {
		statuses = append(statuses, s)
	}
	sort.Slice(statuses, func(i, j int) bool {
		ri, oki := rank[statuses[i]]
		rj, okj := rank[statuses[j]]
		if oki && okj {
			return ri < rj
		}
		if oki {
			return true
		}
		if okj {
			return false
		}
		return statuses[i] < statuses[j]
	})
	return statuses
}

func priorityAbbr(p string) string {
	switch strings.ToLower(p) {
	case "highest":
		return "P1"
	case "high":
		return "P2"
	case "medium":
		return "P3"
	case "low":
		return "P4"
	case "lowest":
		return "P5"
	default:
		return "  "
	}
}

func sinceLastBusinessDay() time.Time {
	now := time.Now()
	if now.Weekday() == time.Monday {
		return now.AddDate(0, 0, -3).Truncate(24 * time.Hour)
	}
	return now.AddDate(0, 0, -1).Truncate(24 * time.Hour)
}

func sprintDayInfo(sprint domain.Sprint) string {
	if sprint.StartDate == "" || sprint.EndDate == "" {
		return ""
	}
	start, err := time.Parse("2006-01-02", sprint.StartDate)
	if err != nil {
		return ""
	}
	end, err := time.Parse("2006-01-02", sprint.EndDate)
	if err != nil {
		return ""
	}
	now := time.Now()
	dayNum := int(now.Sub(start).Hours()/24) + 1
	totalDays := int(end.Sub(start).Hours()/24) + 1
	if dayNum < 1 {
		dayNum = 1
	}
	return fmt.Sprintf("(Day %d of %d)", dayNum, totalDays)
}

// resolveFilterOrJQL returns a cmd that yields FilterResult (or SprintIssuesResult directly
// for empty/project-fallback cases). Date gating is applied later in the FilterResult handler.
func resolveFilterOrJQL(section, value, sinceStr string, team config.TeamConfig, client *jira.Client) tea.Cmd {
	if value == "" {
		if len(team.Projects) == 0 {
			return func() tea.Msg { return jira.SprintIssuesResult{Section: section} }
		}
		projClause := buildProjectInClause(team.Projects)
		jql := fmt.Sprintf(`issuetype = Bug AND project in (%s) AND created >= "%s" ORDER BY created DESC`, projClause, sinceStr)
		return client.FetchSprintIssues(section, jql, 20)
	}
	if _, err := strconv.Atoi(value); err == nil {
		return client.FetchFilterJQL(section, value)
	}
	return func() tea.Msg { return jira.FilterResult{Section: section, JQL: value} }
}

// resolveAllOpenJQL fetches all open issues for a filter or raw JQL without date gating.
// For filter IDs, FilterResult is returned first so the Update handler can strip ORDER BY
// before firing the paginated fetch. For raw JQL, same path via synthetic FilterResult.
func resolveAllOpenJQL(section, value string, client *jira.Client) tea.Cmd {
	if _, err := strconv.Atoi(value); err == nil {
		return client.FetchFilterJQL(section, value)
	}
	return func() tea.Msg { return jira.FilterResult{Section: section, JQL: value} }
}

func stripOrderBy(jql string) string {
	if idx := strings.Index(strings.ToLower(jql), " order by "); idx >= 0 {
		return strings.TrimSpace(jql[:idx])
	}
	return jql
}

func buildProjectInClause(projects []string) string {
	quoted := make([]string, len(projects))
	for i, p := range projects {
		quoted[i] = `"` + p + `"`
	}
	return strings.Join(quoted, ", ")
}

func buildStatusInClause(statuses []string) string {
	quoted := make([]string, len(statuses))
	for i, s := range statuses {
		quoted[i] = `"` + s + `"`
	}
	return strings.Join(quoted, ", ")
}
