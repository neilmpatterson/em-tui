package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/neilmpatterson/em-tui/internal/cache"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
	"github.com/neilmpatterson/em-tui/internal/git"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

type statsState int

const (
	statsStateLoading statsState = iota
	statsStateReport
	statsStateError
)

type statsLoadPhase int

const (
	statsPhaseInit        statsLoadPhase = iota // sprints + history + git + categories
	statsPhaseChangelogs                        // fetching changelogs after history arrives
	statsPhaseReady                             // all data in
)

type StatsModel struct {
	cfg        *config.Config
	team       config.TeamConfig
	member     domain.TeamMember
	jiraClient *jira.Client

	state    statsState
	errMsg   string
	loadPhase statsLoadPhase

	// Pending count: each async op adds one; completion removes one.
	// When it reaches zero we compute and render.
	pending int

	// Per-category load flags
	historyLoaded    bool
	sprintsLoaded    bool
	categoriesLoaded bool
	gitPending       int
	reportsExpected  int
	reportsReceived  int
	changelogsLoaded bool

	// Raw data
	historicalIssues []domain.JiraIssue
	catByID          map[string]string // status ID → category name
	reports          []domain.SprintReportData
	dirs             []domain.DirStat
	commitsTotal     int
	changelogs       []domain.IssueChangelog

	// Computed
	stats domain.DeveloperStats

	viewport viewport.Model
	ready    bool
	width    int
	height   int
}

func NewStats(cfg *config.Config, teamIdx int, jiraClient *jira.Client, memberIdx int, width, height int) StatsModel {
	team := config.TeamConfig{}
	if teamIdx < len(cfg.Teams) {
		team = cfg.Teams[teamIdx]
	}
	var member domain.TeamMember
	if memberIdx < len(team.Members) {
		member = team.Members[memberIdx]
	}

	const headerLines, footerLines = 2, 3
	vpHeight := height - headerLines - footerLines
	if vpHeight < 5 {
		vpHeight = 5
	}
	vp := viewport.New(width, vpHeight)

	// Count expected async loads up front. Init() is a value receiver so
	// mutations to m inside Init don't persist — pending must be initialised
	// here in the constructor, which is the value that Update() will see.
	// 3 fixed loads: history + sprints + status categories.
	// Sprint-report loads are added dynamically when SprintsResult arrives.
	gitPending := 0
	if member.Email != "" {
		gitPending = len(team.Repos)
	}
	pending := 3 + gitPending

	return StatsModel{
		cfg:        cfg,
		team:       team,
		member:     member,
		jiraClient: jiraClient,
		viewport:   vp,
		ready:      width > 0 && height > 0,
		width:      width,
		height:     height,
		pending:    pending,
		gitPending: gitPending,
	}
}

func (m StatsModel) Init() tea.Cmd {
	var cmds []tea.Cmd

	// 1. 18-month historical issues (cached 4h; loads from cache or Jira).
	// pending for these three fixed loads was set in NewStats — do NOT increment
	// m.pending here (Init is a value receiver; mutations are discarded).
	if issues, err := cache.LoadHistoricalIssues(m.member.AccountID); err == nil && len(issues) > 0 {
		cached := issues
		cmds = append(cmds, func() tea.Msg {
			return jira.SprintIssuesResult{Section: jira.SectionStatsHistory, Issues: cached}
		})
	} else {
		cmds = append(cmds, m.jiraClient.FetchHistoricalIssues(m.member.AccountID, 18))
	}

	// 2. Recent sprints (18 months ≈ 39 two-week sprints, fetch 50 for buffer)
	cmds = append(cmds, m.jiraClient.FetchRecentSprints(m.team.BoardID, 50))

	// 3. Status categories for cycle time phase classification
	cmds = append(cmds, m.jiraClient.FetchStatusCategories())

	// 4. Git directory heat map (one per configured repo; gitPending set in NewStats)
	since := time.Now().AddDate(0, -18, 0)
	for _, repo := range m.team.Repos {
		if m.member.Email == "" {
			continue
		}
		cmds = append(cmds, git.FetchCommitDirs(m.member.AccountID, repo, m.member.Email, since))
	}

	return tea.Batch(cmds...)
}

func (m StatsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.state == statsStateReport {
			m.viewport.SetContent(m.renderReport())
		}
		return m, nil

	case tea.KeyMsg:
		if m.state == statsStateReport {
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return GoBack{} }
			}
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if msg.String() == "esc" {
			return m, func() tea.Msg { return GoBack{} }
		}

	case jira.SprintIssuesResult:
		if msg.Section != jira.SectionStatsHistory {
			return m, nil
		}
		m.pending--
		if msg.Err != nil {
			// Non-fatal: show what we have without historical breakdown
			m.historyLoaded = true
		} else {
			m.historicalIssues = msg.Issues
			m.historyLoaded = true
			// Cache for future visits
			if len(msg.Issues) > 0 {
				go func() { _ = cache.SaveHistoricalIssues(m.member.AccountID, msg.Issues) }()
			}
		}
		// Now fetch changelogs for flow computation
		cmd := m.maybeFetchChangelogs()
		return m, tea.Batch(cmd, m.maybeFinish())

	case jira.SprintsResult:
		m.pending--
		m.sprintsLoaded = true
		if msg.Err != nil {
			return m, m.maybeFinish()
		}
		// Fetch closed sprint reports to build velocity history
		var cmds []tea.Cmd
		for _, s := range msg.Sprints {
			if s.State == "future" {
				continue
			}
			if s.State == "active" {
				cmds = append(cmds, m.jiraClient.FetchSprintReport(m.team.BoardID, s))
			} else {
				// closed: load from cache or fetch
				if r, err := cache.LoadSprint(m.team.BoardID, s.ID); err == nil && r != nil {
					cached := *r
					cmds = append(cmds, func() tea.Msg { return jira.SprintReportResult{Data: cached} })
				} else {
					cmds = append(cmds, m.jiraClient.FetchSprintReport(m.team.BoardID, s))
				}
			}
			m.reportsExpected++
			m.pending++
		}
		return m, tea.Batch(cmds...)

	case jira.SprintReportResult:
		m.pending--
		m.reportsReceived++
		if msg.Err == nil {
			m.reports = append(m.reports, msg.Data)
			if msg.Data.Sprint.State == "closed" {
				_ = cache.SaveSprint(m.team.BoardID, msg.Data.Sprint.ID, msg.Data)
			}
		}
		return m, m.maybeFinish()

	case jira.StatusCategoriesResult:
		m.pending--
		m.categoriesLoaded = true
		if msg.Err == nil {
			m.catByID = msg.ByID
		}
		return m, m.maybeFinish()

	case git.CommitDirResult:
		if msg.AccountID != m.member.AccountID {
			return m, nil
		}
		m.pending--
		m.gitPending--
		if msg.Err == nil {
			m.dirs = mergeDirs(m.dirs, msg.Dirs)
			m.commitsTotal += dirTotal(msg.Dirs)
		}
		return m, m.maybeFinish()

	case jira.ChangelogsResult:
		if msg.AccountID != m.member.AccountID {
			return m, nil
		}
		m.pending--
		m.changelogsLoaded = true
		if msg.Err == nil {
			m.changelogs = msg.Changelogs
		}
		return m, m.maybeFinish()

	case viewport.Model:
		m.viewport = msg
	}

	if m.state == statsStateReport {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *StatsModel) maybeFetchChangelogs() tea.Cmd {
	if !m.historyLoaded || m.changelogsLoaded || m.loadPhase >= statsPhaseChangelogs {
		return nil
	}
	m.loadPhase = statsPhaseChangelogs
	keys := make([]string, 0, len(m.historicalIssues))
	for _, iss := range m.historicalIssues {
		keys = append(keys, iss.Key)
	}
	if len(keys) == 0 {
		m.changelogsLoaded = true
		return nil
	}
	m.pending++
	return m.jiraClient.FetchChangelogs(m.member.AccountID, keys)
}

func (m *StatsModel) maybeFinish() tea.Cmd {
	if m.pending > 0 {
		return nil
	}
	m.loadPhase = statsPhaseReady
	m.buildStats()
	m.state = statsStateReport
	if m.ready {
		m.viewport.SetContent(m.renderReport())
	}
	return nil
}

func (m *StatsModel) buildStats() {
	const windowMonths = 18

	// Issue type breakdown
	byType := make(map[string]int)
	for _, iss := range m.historicalIssues {
		t := iss.IssueType
		if t == "" {
			t = "Other"
		}
		byType[t]++
	}

	// Sprint history for this member
	spHistory := memberStatsFromReports(m.member.DisplayName, m.reports)

	// Flow metrics from changelogs
	fc := flowComputer{catByID: m.catByID, phases: m.team.EffectivePhases()}
	openKeys := map[string]bool{} // historical issues are all resolved
	since := time.Now().AddDate(0, -windowMonths, 0)
	flow := fc.computeFlow(m.changelogs, openKeys, since, time.Now())

	// Sort dirs descending
	sort.Slice(m.dirs, func(i, j int) bool { return m.dirs[i].Commits > m.dirs[j].Commits })

	m.stats = domain.DeveloperStats{
		Member:        m.member,
		WindowMonths:  windowMonths,
		ByIssueType:   byType,
		TotalResolved: len(m.historicalIssues),
		SprintHistory: spHistory,
		Flow:          flow,
		CommitsByDir:  m.dirs,
		CommitsTotal:  m.commitsTotal,
	}
}

// --- rendering ---

var (
	statsHeaderStyle = lipgloss.NewStyle().Bold(true)
	statsDimStyle    = lipgloss.NewStyle().Faint(true)
	statsKPIStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	statsSectionHdr  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

func (m StatsModel) View() string {
	member := m.member.DisplayName
	if member == "" {
		member = m.member.AccountID
	}
	title := statsHeaderStyle.Render(fmt.Sprintf("Stats — %s — %s", member, m.team.Name))

	switch m.state {
	case statsStateError:
		return title + "\n\n" + warnStyle.Render(m.errMsg) + "\n\n" + statsDimStyle.Render("esc back")
	case statsStateLoading:
		progress := m.loadingProgress()
		return title + "\n\n" + statsDimStyle.Render(progress) + "\n\n" + statsDimStyle.Render("esc back")
	}

	footer := statsDimStyle.Render("↑/↓ scroll   esc back")
	return lipgloss.JoinVertical(lipgloss.Left, title, m.viewport.View(), "", footer)
}

func (m StatsModel) loadingProgress() string {
	switch m.loadPhase {
	case statsPhaseChangelogs:
		return "Loading changelogs for cycle time analysis…"
	default:
		parts := []string{}
		if !m.historyLoaded {
			parts = append(parts, "ticket history")
		}
		if !m.sprintsLoaded {
			parts = append(parts, "sprint list")
		}
		if m.reportsExpected > 0 && m.reportsReceived < m.reportsExpected {
			parts = append(parts, fmt.Sprintf("sprint reports (%d/%d)", m.reportsReceived, m.reportsExpected))
		}
		if m.gitPending > 0 {
			parts = append(parts, "git history")
		}
		if !m.categoriesLoaded {
			parts = append(parts, "status categories")
		}
		if len(parts) == 0 {
			return "Computing…"
		}
		return "Loading " + strings.Join(parts, ", ") + "…"
	}
}

func (m StatsModel) renderReport() string {
	s := m.stats
	since := time.Now().AddDate(0, -s.WindowMonths, 0)
	window := fmt.Sprintf("%s – %s (%d months)",
		since.Format("Jan 2006"), time.Now().Format("Jan 2006"), s.WindowMonths)

	var sb strings.Builder

	// KPI strip
	reworkRate := 0.0
	if s.TotalResolved > 0 {
		reworkRate = float64(s.Flow.QABounceIssues+s.Flow.ReopenedIssues) / float64(s.TotalResolved) * 100
	}
	avgVelocity := avgSprintVelocity(s.SprintHistory)

	kpi := fmt.Sprintf("%d resolved  ·  %s  ·  %.1f%% rework  ·  %.1f pts/sprint avg",
		s.TotalResolved, issueTypeSummary(s.ByIssueType), reworkRate, avgVelocity)
	sb.WriteString(statsDimStyle.Render(window) + "\n\n")
	sb.WriteString(statsKPIStyle.Render(kpi) + "\n\n")

	// Ticket breakdown
	sb.WriteString(statsSectionHdr.Render("Ticket Breakdown") + "\n")
	sb.WriteString(m.renderTypeTable() + "\n")

	// Sprint velocity
	sb.WriteString(statsSectionHdr.Render("Sprint Velocity") + "\n")
	sb.WriteString(m.renderVelocityChart() + "\n")

	// Cycle time & flow
	sb.WriteString(statsSectionHdr.Render("Cycle Time & Flow") + "\n")
	sb.WriteString(m.renderFlowSection() + "\n")

	// Git heatmap
	if len(s.CommitsByDir) > 0 {
		sb.WriteString(statsSectionHdr.Render("Git Activity") + "\n")
		sb.WriteString(m.renderGitSection() + "\n")
	} else if m.gitPending == 0 && len(m.team.Repos) == 0 {
		sb.WriteString(statsSectionHdr.Render("Git Activity") + "\n")
		sb.WriteString(statsDimStyle.Render("No repos configured for this team.") + "\n\n")
	}

	// Sprint history table
	sb.WriteString(statsSectionHdr.Render("Sprint History") + "\n")
	sb.WriteString(m.renderSprintHistoryTable())

	return sb.String()
}

// renderTypeTable renders ticket breakdown by issue type.
func (m StatsModel) renderTypeTable() string {
	s := m.stats
	if s.TotalResolved == 0 {
		return statsDimStyle.Render("  No resolved issues in window.\n\n")
	}

	// Sort types by count desc
	type typeRow struct{ name string; count int }
	rows := make([]typeRow, 0, len(s.ByIssueType))
	for k, v := range s.ByIssueType {
		rows = append(rows, typeRow{k, v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].name < rows[j].name
	})

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.Style().Color.Header = text.Colors{text.BgCyan, text.FgBlack, text.Bold}
	t.Style().Color.Row = text.Colors{text.FgHiCyan}
	t.Style().Color.Border = text.Colors{text.FgCyan}
	t.AppendHeader(table.Row{"TYPE", "COUNT", "%"})
	for _, r := range rows {
		pct := float64(r.count) / float64(s.TotalResolved) * 100
		t.AppendRow(table.Row{r.name, r.count, fmt.Sprintf("%.0f%%", pct)})
	}
	return t.Render() + "\n\n"
}

// renderVelocityChart renders a horizontal bar chart of sprint completed SP.
func (m StatsModel) renderVelocityChart() string {
	s := m.stats
	if len(s.SprintHistory) == 0 {
		return statsDimStyle.Render("  No sprint data available.\n\n")
	}

	// Only closed sprints for the chart; active contributes partial data
	var closed []domain.MemberSprintStats
	for _, sp := range s.SprintHistory {
		if strings.ToLower(sp.Sprint.State) != "active" {
			closed = append(closed, sp)
		}
	}

	var maxSP float64
	for _, sp := range closed {
		if sp.CompletedSP > maxSP {
			maxSP = sp.CompletedSP
		}
	}
	if maxSP == 0 {
		return statsDimStyle.Render("  Sprint data has no story points.\n\n")
	}

	barWidth := m.width - 30
	if barWidth < 10 {
		barWidth = 10
	}
	if barWidth > 60 {
		barWidth = 60
	}

	var sb strings.Builder
	// Show up to 24 most recent sprints
	display := closed
	if len(display) > 24 {
		display = display[len(display)-24:]
	}
	for _, sp := range display {
		bars := int(math.Round(sp.CompletedSP / maxSP * float64(barWidth)))
		bar := strings.Repeat("█", bars)
		name := sp.Sprint.Name
		if len(name) > 18 {
			name = name[:15] + "…"
		}
		line := fmt.Sprintf("  %-18s %5.1f  %s", name, sp.CompletedSP, bar)
		sb.WriteString(line + "\n")
	}

	ra, ha, signal := spTrend(s.SprintHistory)
	trendStr := "stable"
	switch signal {
	case "up":
		trendStr = "↑ improving"
	case "down":
		trendStr = "↓ declining"
	}
	avg := avgSprintVelocity(closed)
	summary := fmt.Sprintf("  Avg: %.1f  Recent: %.1f  Historical: %.1f  Trend: %s", avg, ra, ha, trendStr)
	sb.WriteString(statsDimStyle.Render(summary) + "\n\n")
	return sb.String()
}

// renderFlowSection renders cycle time and phase breakdown.
func (m StatsModel) renderFlowSection() string {
	f := m.stats.Flow
	if !m.changelogsLoaded || f.IssuesCompleted == 0 {
		if !m.changelogsLoaded {
			return statsDimStyle.Render("  Cycle time unavailable (changelogs not loaded).\n\n")
		}
		return statsDimStyle.Render("  No completed cycles with timing data.\n\n")
	}

	var sb strings.Builder

	// Summary line
	sb.WriteString(fmt.Sprintf("  P50 %s  P90 %s  Throughput %.1f/wk  (%d issues analyzed)\n",
		fmtDays(f.P50CycleDays), fmtDays(f.P90CycleDays), f.ThroughputPerWeek, f.IssuesCompleted))

	// Phase breakdown bars
	total := f.AvgPhase.Total()
	if total > 0 {
		sb.WriteString("\n")
		for _, p := range domain.PhaseOrder {
			d, ok := f.AvgPhase[p]
			if !ok || d < 0.05 {
				continue
			}
			share := d / total
			bars := int(math.Round(share * 30))
			bar := strings.Repeat("▪", bars)
			sb.WriteString(fmt.Sprintf("  %-18s %5s  %s\n",
				p.String(), fmtDays(d), bar))
		}
		sb.WriteString("\n")
	}

	// Rework / reopens
	if f.ReopenedIssues > 0 || f.QABounceIssues > 0 {
		rePct := 0.0
		if m.stats.TotalResolved > 0 {
			rePct = float64(f.QABounceIssues+f.ReopenedIssues) / float64(m.stats.TotalResolved) * 100
		}
		sb.WriteString(fmt.Sprintf("  Rework: %d issues bounced from QA/review (%.0f%%)  Reopened: %d\n",
			f.QABounceIssues, rePct, f.ReopenedIssues))
	}
	if f.P50ReviewDays > 0 {
		sb.WriteString(fmt.Sprintf("  Review wait P50: %s\n", fmtDays(f.P50ReviewDays)))
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderGitSection renders the directory heatmap.
func (m StatsModel) renderGitSection() string {
	dirs := m.stats.CommitsByDir
	total := m.stats.CommitsTotal
	if total == 0 || len(dirs) == 0 {
		return statsDimStyle.Render("  No commit data.\n\n")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  %d commits across %d directories\n\n", total, len(dirs)))

	display := dirs
	if len(display) > 15 {
		display = display[:15]
	}
	var maxC int
	for _, d := range display {
		if d.Commits > maxC {
			maxC = d.Commits
		}
	}

	barWidth := m.width - 32
	if barWidth < 10 {
		barWidth = 10
	}
	if barWidth > 50 {
		barWidth = 50
	}

	for _, d := range display {
		bars := int(math.Round(float64(d.Commits) / float64(maxC) * float64(barWidth)))
		bar := strings.Repeat("█", bars)
		pct := float64(d.Commits) / float64(total) * 100
		line := fmt.Sprintf("  %-18s %4d  %4.0f%%  %s", d.Dir, d.Commits, pct, bar)
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderSprintHistoryTable renders sprint history as a go-pretty table.
func (m StatsModel) renderSprintHistoryTable() string {
	s := m.stats
	if len(s.SprintHistory) == 0 {
		return statsDimStyle.Render("  No sprint history.\n\n")
	}

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.Style().Color.Header = text.Colors{text.BgBlue, text.FgBlack, text.Bold}
	t.Style().Color.Row = text.Colors{text.FgHiBlue}
	t.Style().Color.Border = text.Colors{text.FgBlue}
	t.AppendHeader(table.Row{"SPRINT", "PLAN", "DONE", "%", "CARRY"})

	// Most recent first
	hist := s.SprintHistory
	for i := len(hist) - 1; i >= 0; i-- {
		sp := hist[i]
		plan := sp.CompletedSP + sp.IncompleteSP
		var pct string
		if plan > 0 {
			pct = fmt.Sprintf("%.0f%%", sp.CompletedSP/plan*100)
		} else {
			pct = "—"
		}
		name := sp.Sprint.Name
		if len(name) > 28 {
			name = name[:25] + "…"
		}
		t.AppendRow(table.Row{name,
			spVal(plan), spVal(sp.CompletedSP), pct, spVal(sp.IncompleteSP)})
	}
	return t.Render() + "\n\n"
}

// --- helpers ---

func issueTypeSummary(byType map[string]int) string {
	if len(byType) == 0 {
		return "no data"
	}
	// Sort by count desc for readability
	type kv struct{ k string; v int }
	pairs := make([]kv, 0, len(byType))
	for k, v := range byType {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%d %s", p.v, strings.ToLower(p.k)+"s"))
	}
	return strings.Join(parts, " / ")
}

func avgSprintVelocity(stats []domain.MemberSprintStats) float64 {
	var sum float64
	var n int
	for _, s := range stats {
		if strings.ToLower(s.Sprint.State) == "active" {
			continue
		}
		sum += s.CompletedSP
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func spVal(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f", v)
}

// mergeDirs adds src dir counts into dst, deduplicating by dir name.
func mergeDirs(dst, src []domain.DirStat) []domain.DirStat {
	idx := make(map[string]int, len(dst))
	for i, d := range dst {
		idx[d.Dir] = i
	}
	for _, d := range src {
		if i, ok := idx[d.Dir]; ok {
			dst[i].Commits += d.Commits
		} else {
			idx[d.Dir] = len(dst)
			dst = append(dst, d)
		}
	}
	return dst
}

// dirTotal sums commits across all dirs.
func dirTotal(dirs []domain.DirStat) int {
	var total int
	for _, d := range dirs {
		total += d.Commits
	}
	return total
}
