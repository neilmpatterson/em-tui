package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/neilmpatterson/em-tui/internal/cache"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

// sectionStyle holds go-pretty color config for a sprint issue section.
type sectionStyle struct {
	header text.Colors
	row    text.Colors
	border text.Colors
}

var (
	completedSectionStyle = sectionStyle{
		header: text.Colors{text.BgGreen, text.FgBlack, text.Bold},
		row:    text.Colors{text.FgHiGreen},
		border: text.Colors{text.FgGreen},
	}
	notCompletedSectionStyle = sectionStyle{
		header: text.Colors{text.BgYellow, text.FgBlack, text.Bold},
		row:    text.Colors{text.FgHiYellow},
		border: text.Colors{text.FgYellow},
	}
)

type sprintViewState int

const (
	srStateLoadingSprints sprintViewState = iota
	srStateSprintList
	srStateLoadingReport
	srStateReport
	srStateError
)

type SprintReportModel struct {
	cfg        *config.Config
	team       config.TeamConfig
	jiraClient *jira.Client

	state  sprintViewState
	errMsg string

	sprints        []domain.Sprint
	cursor         int
	selectedSprint domain.Sprint

	report domain.SprintReportData

	viewport      viewport.Model
	ready         bool
	width, height int
}

func NewSprintReport(cfg *config.Config, teamIdx int, jiraClient *jira.Client, width, height int) SprintReportModel {
	team := config.TeamConfig{}
	if teamIdx < len(cfg.Teams) {
		team = cfg.Teams[teamIdx]
	}

	const headerLines, footerLines = 2, 3
	vpHeight := height - headerLines - footerLines
	if vpHeight < 5 {
		vpHeight = 5
	}
	vp := viewport.New(width, vpHeight)

	m := SprintReportModel{
		cfg:        cfg,
		team:       team,
		jiraClient: jiraClient,
		viewport:   vp,
		ready:      width > 0 && height > 0,
		width:      width,
		height:     height,
	}
	switch {
	case strings.ToLower(team.BoardType) == "kanban":
		m.state = srStateError
		m.errMsg = "Sprint reports are not available for Kanban boards."
	case team.BoardID == 0:
		m.state = srStateError
		m.errMsg = "No Scrum board configured for this team."
	}
	return m
}

func (m SprintReportModel) Init() tea.Cmd {
	if m.state == srStateError || m.jiraClient == nil {
		return nil
	}
	return m.jiraClient.FetchRecentSprints(m.team.BoardID, 10)
}

func (m SprintReportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.state == srStateReport {
			m.viewport.SetContent(m.renderReport())
		}
		return m, nil

	case jira.SprintsResult:
		if msg.Err != nil {
			m.state = srStateError
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.sprints = msg.Sprints
		m.state = srStateSprintList
		return m, nil

	case jira.SprintReportResult:
		if msg.Err != nil {
			m.state = srStateError
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.report = msg.Data
		m.state = srStateReport
		if m.ready {
			m.viewport.SetContent(m.renderReport())
			m.viewport.GotoTop()
		}
		// Persist closed sprint reports so future loads skip the API call.
		var saveCmd tea.Cmd
		if strings.ToLower(m.report.Sprint.State) == "closed" {
			boardID := m.team.BoardID
			report := m.report
			saveCmd = func() tea.Msg {
				_ = cache.SaveSprint(boardID, report.Sprint.ID, report)
				return nil
			}
		}
		return m, saveCmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m SprintReportModel) handleKey(msg tea.KeyMsg) (SprintReportModel, tea.Cmd) {
	switch m.state {
	case srStateSprintList:
		return m.handleListKey(msg)
	case srStateReport:
		return m.handleReportKey(msg)
	default:
		if msg.String() == "q" || msg.String() == "esc" {
			return m, func() tea.Msg { return GoBack{} }
		}
	}
	return m, nil
}

func (m SprintReportModel) handleListKey(msg tea.KeyMsg) (SprintReportModel, tea.Cmd) {
	n := len(m.sprints)
	switch msg.String() {
	case "q", "esc":
		return m, func() tea.Msg { return GoBack{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < n-1 {
			m.cursor++
		}
	case "enter", " ":
		if n == 0 {
			break
		}
		m.selectedSprint = m.sprints[m.cursor]
		m.state = srStateLoadingReport
		return m, fetchOrLoadCached(m.team.BoardID, m.selectedSprint, m.jiraClient)
	}
	return m, nil
}

func (m SprintReportModel) handleReportKey(msg tea.KeyMsg) (SprintReportModel, tea.Cmd) {
	switch msg.String() {
	case "b", "esc":
		m.state = srStateSprintList
		return m, nil
	case "q":
		return m, func() tea.Msg { return GoBack{} }
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m SprintReportModel) View() string {
	header := m.headerView() + "\n"

	switch m.state {
	case srStateLoadingSprints:
		return header + dimStyle.Render("  Loading sprints...") + "\n\n" + dimStyle.Render("  q back")

	case srStateSprintList:
		return header + m.renderSprintList() + "\n\n" + dimStyle.Render("  j/k select   Enter open   q back")

	case srStateLoadingReport:
		return header + dimStyle.Render("  Loading report...") + "\n\n" + dimStyle.Render("  q back")

	case srStateReport:
		footer := "\n" + dimStyle.Render("  j/k scroll   b list   q menu")
		if !m.ready {
			return header + dimStyle.Render("  Initializing...") + footer
		}
		return header + m.viewport.View() + footer

	case srStateError:
		return header + warnStyle.Render("  ⚠ "+m.errMsg) + "\n\n" + dimStyle.Render("  q back")
	}
	return header
}

func (m SprintReportModel) headerView() string {
	base := titleStyle.Render("Sprint Report") + dimStyle.Render("  —  ") + titleStyle.Render(m.team.Name)
	switch m.state {
	case srStateLoadingReport:
		if m.selectedSprint.Name != "" {
			return base + dimStyle.Render("  —  "+m.selectedSprint.Name)
		}
	case srStateReport:
		if m.report.Sprint.Name != "" {
			return base + dimStyle.Render("  —  "+m.report.Sprint.Name)
		}
	}
	return base
}

func (m SprintReportModel) renderSprintList() string {
	if len(m.sprints) == 0 {
		return dimStyle.Render("  No sprints found.")
	}
	var sb strings.Builder
	for i, s := range m.sprints {
		prefix := "  "
		style := normalStyle
		if i == m.cursor {
			prefix = "> "
			style = selectedStyle
		}
		stateLabel := strings.ToLower(s.State)
		if stateLabel == "active" {
			stateLabel = "ACTIVE"
		}
		dates := fmt.Sprintf("%s – %s", shortDate(s.StartDate), shortDate(s.EndDate))
		line := fmt.Sprintf("%s%-36s  %-18s  %s", prefix, s.Name, dates, stateLabel)
		sb.WriteString(style.Render(line) + "\n")
	}
	return sb.String()
}

func (m SprintReportModel) renderReport() string {
	r := m.report
	w := m.width
	if w < 80 {
		w = 100
	}

	var sb strings.Builder

	stateLabel := strings.ToUpper(r.Sprint.State)
	dates := fmt.Sprintf("%s – %s", shortDate(r.Sprint.StartDate), shortDate(r.Sprint.EndDate))
	sb.WriteString(dimStyle.Render(fmt.Sprintf("  %s   [%s]", dates, stateLabel)) + "\n")
	sb.WriteString("  " + strings.Repeat("─", w-4) + "\n\n")

	// Velocity / story points summary
	if r.PlannedSP != nil || r.CompletedSP != nil {
		sb.WriteString(m.renderVelocity(r) + "\n")
	}

	// Issue completion overview
	total := len(r.Completed) + len(r.NotCompleted)
	if total > 0 {
		pct := (len(r.Completed) * 100) / total
		sb.WriteString(normalStyle.Render(fmt.Sprintf(
			"  Issues:  %d of %d completed (%d%%)",
			len(r.Completed), total, pct,
		)) + "\n\n")
	}

	// Contributor breakdown
	if len(r.Completed)+len(r.NotCompleted) > 0 {
		sb.WriteString(m.renderContributors(r) + "\n")
	}

	sb.WriteString(m.renderIssueSection("Completed", r.Completed, r.AddedMidSprint, w, completedSectionStyle))
	sb.WriteString(m.renderIssueSection("Not Completed", r.NotCompleted, r.AddedMidSprint, w, notCompletedSectionStyle))
	if len(r.Punted) > 0 {
		sb.WriteString(m.renderIssueSection("Removed from Sprint", r.Punted, nil, w, sectionStyle{}))
	}

	return sb.String()
}

func spStr(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatSP(*v)
}

func (m SprintReportModel) renderVelocity(r domain.SprintReportData) string {
	// Split SP into "original commitment" vs "added mid-sprint" populations.
	// This makes the committed→delivered story coherent and separates scope changes.
	var origCompletedSP, addedCompletedSP, origOpenSP, addedOpenSP float64
	hasSPBreakdown := false

	for _, iss := range r.Completed {
		if iss.FinalSP == nil {
			continue
		}
		hasSPBreakdown = true
		if r.AddedMidSprint[iss.Key] {
			addedCompletedSP += *iss.FinalSP
		} else {
			origCompletedSP += *iss.FinalSP
		}
	}
	for _, iss := range r.NotCompleted {
		if iss.FinalSP == nil {
			continue
		}
		hasSPBreakdown = true
		if r.AddedMidSprint[iss.Key] {
			addedOpenSP += *iss.FinalSP
		} else {
			origOpenSP += *iss.FinalSP
		}
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("  Velocity") + "\n")

	// Committed → Delivered row (original issues only)
	if r.PlannedSP != nil || hasSPBreakdown {
		planned := spStr(r.PlannedSP)
		var deliveredStr, pctStr string
		if hasSPBreakdown {
			deliveredStr = formatSP(origCompletedSP)
			if r.PlannedSP != nil && *r.PlannedSP > 0 {
				pct := int((origCompletedSP / *r.PlannedSP) * 100)
				pctStr = fmt.Sprintf(" (%d%%)", pct)
			}
		} else {
			deliveredStr = spStr(r.CompletedSP)
			if r.PlannedSP != nil && r.CompletedSP != nil && *r.PlannedSP > 0 {
				pct := int((*r.CompletedSP / *r.PlannedSP) * 100)
				pctStr = fmt.Sprintf(" (%d%%)", pct)
			}
		}

		openStr := "0"
		if hasSPBreakdown {
			openStr = formatSP(origOpenSP)
		}

		sb.WriteString(normalStyle.Render(fmt.Sprintf(
			"  Committed: %-6s   Delivered: %-6s%s   Open (of committed): %s",
			planned, deliveredStr, pctStr, openStr,
		)) + "\n")
	}

	// Mid-sprint scope row — only shown when scope changed
	totalAdded := addedCompletedSP + addedOpenSP
	addedCount := len(r.AddedMidSprint)
	if hasSPBreakdown && totalAdded > 0 {
		sb.WriteString(dimStyle.Render(fmt.Sprintf(
			"  Mid-sprint: +%s added (%d issues)   %s completed   %s still open",
			formatSP(totalAdded), addedCount,
			formatSP(addedCompletedSP), formatSP(addedOpenSP),
		)) + "\n")
	} else if addedCount > 0 || len(r.Punted) > 0 {
		// No SP data — fall back to issue counts
		var parts []string
		if addedCount > 0 {
			parts = append(parts, fmt.Sprintf("+%d issues added mid-sprint", addedCount))
		}
		if len(r.Punted) > 0 {
			parts = append(parts, fmt.Sprintf("%d issues removed", len(r.Punted)))
		}
		sb.WriteString(dimStyle.Render("  Scope:      "+strings.Join(parts, "   ")) + "\n")
	}

	return sb.String()
}

type contributorStat struct {
	name            string
	completedCount  int
	incompleteCount int
	completedSP     float64
	incompleteSP    float64
	hasSP           bool
}

func (m SprintReportModel) renderContributors(r domain.SprintReportData) string {
	stats := make(map[string]*contributorStat)

	add := func(name string, done bool, sp *float64) {
		if name == "" {
			name = "(unassigned)"
		}
		s, ok := stats[name]
		if !ok {
			s = &contributorStat{name: name}
			stats[name] = s
		}
		if done {
			s.completedCount++
			if sp != nil {
				s.completedSP += *sp
				s.hasSP = true
			}
		} else {
			s.incompleteCount++
			if sp != nil {
				s.incompleteSP += *sp
				s.hasSP = true
			}
		}
	}

	for _, iss := range r.Completed {
		add(iss.Assignee, true, iss.FinalSP)
	}
	for _, iss := range r.NotCompleted {
		add(iss.Assignee, false, iss.FinalSP)
	}

	if len(stats) == 0 {
		return ""
	}

	// Sort by completed SP desc, then completed count desc
	sorted := make([]*contributorStat, 0, len(stats))
	for _, s := range stats {
		sorted = append(sorted, s)
	}
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			a, b := sorted[j-1], sorted[j]
			swap := false
			if a.hasSP && b.hasSP {
				swap = a.completedSP < b.completedSP
			} else {
				swap = a.completedCount < b.completedCount
			}
			if swap {
				sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
			} else {
				break
			}
		}
	}

	hasSP := false
	for _, s := range sorted {
		if s.hasSP {
			hasSP = true
			break
		}
	}

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	if hasSP {
		t.AppendHeader(table.Row{"ASSIGNEE", "COMPLETED", "INCOMPLETE"})
	} else {
		t.AppendHeader(table.Row{"ASSIGNEE", "DONE", "INCOMPLETE"})
	}
	for _, s := range sorted {
		var doneStr, openStr string
		if hasSP {
			doneStr = fmt.Sprintf("%d issues / %s pts", s.completedCount, formatSP(s.completedSP))
			if s.incompleteCount > 0 {
				openStr = fmt.Sprintf("%d issues / %s pts", s.incompleteCount, formatSP(s.incompleteSP))
			} else {
				openStr = "—"
			}
		} else {
			doneStr = fmt.Sprintf("%d", s.completedCount)
			if s.incompleteCount > 0 {
				openStr = fmt.Sprintf("%d", s.incompleteCount)
			} else {
				openStr = "—"
			}
		}
		t.AppendRow(table.Row{s.name, doneStr, openStr})
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("  Contributors") + "\n")
	for _, line := range strings.Split(t.Render(), "\n") {
		if line != "" {
			sb.WriteString("  " + line + "\n")
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

func (m SprintReportModel) renderIssueSection(title string, issues []domain.SprintIssueCompact, addedKeys map[string]bool, w int, style sectionStyle) string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render(fmt.Sprintf("  %s  (%d)", title, len(issues))) + "\n")

	if len(issues) == 0 {
		sb.WriteString(dimStyle.Render("  (none)") + "\n\n")
		return sb.String()
	}

	// Fixed: mark(1)+key(11)+assignee(16)+status(16)+priority(8)+sp(4) + borders/gaps ~24
	summaryW := w - 1 - 11 - 16 - 16 - 8 - 4 - 24
	if summaryW < 20 {
		summaryW = 20
	}

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	if len(style.header) > 0 {
		t.Style().Color.Header = style.header
	}
	if len(style.row) > 0 {
		t.Style().Color.Row = style.row
	}
	if len(style.border) > 0 {
		t.Style().Color.Border = style.border
	}
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, WidthMax: 1},
		{Number: 2, WidthMax: 11},
		{Number: 3, WidthMax: 16},
		{Number: 4, WidthMax: 16},
		{Number: 5, WidthMax: 8},
		{Number: 6, WidthMax: 4},
		{Number: 7, WidthMax: summaryW},
	})
	t.AppendHeader(table.Row{"", "KEY", "ASSIGNEE", "STATUS", "PRIORITY", "SP", "SUMMARY"})

	for _, iss := range issues {
		mark := ""
		if addedKeys != nil && addedKeys[iss.Key] {
			mark = "+"
		}
		sp := "—"
		if iss.FinalSP != nil {
			sp = formatSP(*iss.FinalSP)
		}
		priority := iss.Priority
		if priority == "" {
			priority = "—"
		}
		t.AppendRow(table.Row{
			mark,
			iss.Key,
			iss.Assignee,
			iss.Status,
			priority,
			sp,
			iss.Summary,
		})
	}

	for _, line := range strings.Split(t.Render(), "\n") {
		if line != "" {
			sb.WriteString("  " + line + "\n")
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// fetchOrLoadCached returns a Cmd that serves a closed sprint report from the local
// cache if available, falling back to a live Jira fetch. Active sprints always fetch live.
func fetchOrLoadCached(boardID int, sprint domain.Sprint, client *jira.Client) tea.Cmd {
	return func() tea.Msg {
		if strings.ToLower(sprint.State) == "closed" {
			if data, err := cache.LoadSprint(boardID, sprint.ID); err == nil && data != nil {
				return jira.SprintReportResult{Data: *data}
			}
		}
		return client.FetchSprintReport(boardID, sprint)()
	}
}

// shortDate converts "2026-05-06" to "May 6".
func shortDate(s string) string {
	if len(s) < 10 {
		return s
	}
	t, err := time.Parse("2006-01-02", s[:10])
	if err != nil {
		return s
	}
	return t.Format("Jan 2")
}

// formatSP formats a story point float as a compact string: 5.0 → "5", 2.5 → "2.5".
func formatSP(v float64) string {
	if v == float64(int(v)) {
		return fmt.Sprintf("%d", int(v))
	}
	return fmt.Sprintf("%.1f", v)
}
