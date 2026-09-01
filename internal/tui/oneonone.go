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
	"github.com/neilmpatterson/em-tui/internal/cache"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
	"github.com/neilmpatterson/em-tui/internal/git"
	"github.com/neilmpatterson/em-tui/internal/jira"
)

type ooState int

const (
	ooStateLoading ooState = iota
	ooStateReport
	ooStateError
)

type OneOnOneModel struct {
	cfg        *config.Config
	team       config.TeamConfig
	jiraClient *jira.Client
	member     domain.TeamMember

	state  ooState
	errMsg string

	sprints        []domain.Sprint
	sprintReports  []domain.SprintReportData
	pendingReports int
	openIssues     []domain.JiraIssue
	commits        []domain.GitCommit

	sprintsLoaded       bool
	issuesLoaded        bool
	pendingCommits      int
	changelogsRequested bool
	changelogsLoaded    bool

	doraMetrics domain.MemberDORA

	viewport viewport.Model
	ready    bool
	width    int
	height   int
}

func NewOneOnOne(cfg *config.Config, teamIdx int, jiraClient *jira.Client, memberIdx int, width, height int) OneOnOneModel {
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

	m := OneOnOneModel{
		cfg:            cfg,
		team:           team,
		jiraClient:     jiraClient,
		member:         member,
		pendingCommits: len(team.Repos),
		viewport:       vp,
		ready:          width > 0 && height > 0,
		width:          width,
		height:         height,
	}
	if jiraClient == nil {
		m.state = ooStateError
		m.errMsg = "Jira client not configured."
	}
	return m
}

func (m OneOnOneModel) loading() bool {
	if m.state == ooStateError {
		return false
	}
	return !m.sprintsLoaded || !m.issuesLoaded || m.pendingCommits > 0 ||
		m.pendingReports > 0 || !m.changelogsLoaded
}

func (m OneOnOneModel) Init() tea.Cmd {
	if m.jiraClient == nil {
		m.state = ooStateError
		m.errMsg = "Jira client not configured."
		return nil
	}
	since := time.Now().AddDate(0, 0, -56) // 8 weeks
	// Use email for git --author filter; fall back to display name if email not set.
	authorFilter := m.member.Email
	if authorFilter == "" {
		authorFilter = m.member.DisplayName
	}
	cmds := []tea.Cmd{
		m.jiraClient.FetchRecentSprints(m.team.BoardID, 6),
		m.jiraClient.IssuesAssignedTo(m.member.AccountID),
	}
	for _, repo := range m.team.Repos {
		cmds = append(cmds, git.FetchCommits(m.member.AccountID, repo, authorFilter, since))
	}
	return tea.Batch(cmds...)
}

func (m OneOnOneModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.state == ooStateReport {
			m.viewport.SetContent(m.renderContent())
		}
		return m, nil

	case jira.SprintsResult:
		if msg.Err != nil {
			m.state = ooStateError
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.sprints = msg.Sprints
		m.sprintsLoaded = true
		if len(msg.Sprints) == 0 {
			m.pendingReports = 0
			m.changelogsLoaded = true
			m.changelogsRequested = true
			m.finishIfDone()
			return m, nil
		}
		m.pendingReports = len(msg.Sprints)
		boardID := m.team.BoardID
		cmds := make([]tea.Cmd, 0, len(msg.Sprints))
		for _, s := range msg.Sprints {
			sprint := s
			cmds = append(cmds, ooFetchOrLoadCached(boardID, sprint, m.jiraClient))
		}
		return m, tea.Batch(cmds...)

	case jira.SprintReportResult:
		if msg.Err == nil {
			m.sprintReports = append(m.sprintReports, msg.Data)
			if strings.ToLower(msg.Data.Sprint.State) == "closed" {
				boardID := m.team.BoardID
				report := msg.Data
				go func() { _ = cache.SaveSprint(boardID, report.Sprint.ID, report) }()
			}
		}
		if m.pendingReports > 0 {
			m.pendingReports--
		}
		// Fire changelog fetch once all sprint reports are in.
		if m.pendingReports == 0 && !m.changelogsRequested {
			m.changelogsRequested = true
			keys := m.issueKeysForChangelog()
			if len(keys) == 0 {
				m.changelogsLoaded = true
				m.finishIfDone()
				return m, nil
			}
			return m, m.jiraClient.FetchChangelogs(m.member.AccountID, keys)
		}
		m.finishIfDone()
		return m, nil

	case jira.ChangelogsResult:
		if msg.AccountID == m.member.AccountID {
			m.doraMetrics = computeDORA(msg.Changelogs)
		}
		m.changelogsLoaded = true
		m.finishIfDone()
		return m, nil

	case jira.IssueResult:
		if msg.AccountID == m.member.AccountID && msg.Err == nil {
			m.openIssues = msg.Issues
		}
		m.issuesLoaded = true
		m.finishIfDone()
		return m, nil

	case git.CommitResult:
		if msg.Err == nil {
			m.commits = append(m.commits, msg.Commits...)
		}
		if m.pendingCommits > 0 {
			m.pendingCommits--
		}
		m.finishIfDone()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, func() tea.Msg { return GoBack{} }
		default:
			if m.state == ooStateReport {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}
	return m, nil
}

// finishIfDone transitions to ooStateReport once all data is loaded.
// Must be called on a pointer to m (captured via &m in Update's value receiver).
func (m *OneOnOneModel) finishIfDone() {
	if m.loading() || m.state != ooStateLoading {
		return
	}
	m.state = ooStateReport
	if m.ready {
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoTop()
	}
}

func (m OneOnOneModel) View() string {
	teamLabel := ""
	if m.team.Name != "" {
		teamLabel = "  ·  " + m.team.Name
	}
	header := titleStyle.Render("1:1 — "+m.member.DisplayName+teamLabel) + "\n"
	rule := strings.Repeat("─", min(m.width, 60)) + "\n"

	switch m.state {
	case ooStateError:
		return header + rule + "\n" + warnStyle.Render(m.errMsg) + "\n"
	case ooStateLoading:
		return header + rule + "\n" + dimStyle.Render("Loading...") + "\n"
	}

	if !m.ready {
		return header + rule + "\n" + m.renderContent()
	}
	footer := "\n" + dimStyle.Render("j/k scroll   q back")
	return header + rule + m.viewport.View() + footer
}

// ooFetchOrLoadCached checks the disk cache before hitting the API for closed sprints.
func ooFetchOrLoadCached(boardID int, sprint domain.Sprint, client *jira.Client) tea.Cmd {
	if strings.ToLower(sprint.State) == "closed" {
		if cached, err := cache.LoadSprint(boardID, sprint.ID); err == nil && cached != nil {
			data := *cached
			return func() tea.Msg { return jira.SprintReportResult{Data: data} }
		}
	}
	return client.FetchSprintReport(boardID, sprint)
}

// --- Metric helpers ---

// issueKeysForChangelog collects issue keys completed by this member in the last 90 days.
func (m *OneOnOneModel) issueKeysForChangelog() []string {
	cutoff := time.Now().AddDate(0, -3, 0)
	seen := map[string]bool{}
	var keys []string
	for _, r := range m.sprintReports {
		endDate, err := time.Parse("2006-01-02", r.Sprint.EndDate)
		if err != nil || endDate.Before(cutoff) {
			continue
		}
		for _, issue := range r.Completed {
			if issue.Assignee == m.member.DisplayName && !seen[issue.Key] {
				seen[issue.Key] = true
				keys = append(keys, issue.Key)
				if len(keys) >= 50 {
					return keys
				}
			}
		}
	}
	return keys
}

// computeDORA derives cycle time, code review wait, and rework count from issue changelogs.
func computeDORA(changelogs []domain.IssueChangelog) domain.MemberDORA {
	dora := domain.MemberDORA{IssuesAnalyzed: len(changelogs)}
	var cycleTimes, reviewTimes []float64
	for _, cl := range changelogs {
		var firstActive, lastDone *time.Time
		var enterReview *time.Time
		reworked := false
		for i := range cl.Transitions {
			t := &cl.Transitions[i]
			if isActiveStatus(t.To) && firstActive == nil {
				ts := t.Timestamp
				firstActive = &ts
			}
			if isDoneStatus(t.To) {
				ts := t.Timestamp
				lastDone = &ts
			}
			// Code review timing
			if isCodeReviewStatus(t.To) {
				ts := t.Timestamp
				enterReview = &ts
			} else if enterReview != nil && isCodeReviewStatus(t.From) {
				d := t.Timestamp.Sub(*enterReview).Hours() / 24
				if d > 0 {
					reviewTimes = append(reviewTimes, d)
				}
				enterReview = nil
			}
			// Rework detection
			if !reworked && isReworkTransition(t.From, t.To) {
				reworked = true
				dora.ReworkCount++
			}
		}
		if firstActive != nil && lastDone != nil && lastDone.After(*firstActive) {
			cycleTimes = append(cycleTimes, lastDone.Sub(*firstActive).Hours()/24)
		}
	}
	if len(cycleTimes) > 0 {
		var sum float64
		for _, d := range cycleTimes {
			sum += d
		}
		dora.AvgCycleTimeDays = sum / float64(len(cycleTimes))
	}
	if len(reviewTimes) > 0 {
		var sum float64
		for _, d := range reviewTimes {
			sum += d
		}
		dora.AvgCodeReviewDays = sum / float64(len(reviewTimes))
	}
	return dora
}

func isActiveStatus(s string) bool {
	sl := strings.ToLower(s)
	return strings.Contains(sl, "progress") || strings.Contains(sl, "development") ||
		strings.Contains(sl, "started") || sl == "open"
}

func isDoneStatus(s string) bool {
	sl := strings.ToLower(s)
	return sl == "done" || sl == "closed" || sl == "resolved" ||
		strings.Contains(sl, "complete") || strings.Contains(sl, "deploy") ||
		strings.Contains(sl, "release") || strings.Contains(sl, "merged") ||
		strings.Contains(sl, "ship") || strings.Contains(sl, "ready to")
}

func isCodeReviewStatus(s string) bool {
	sl := strings.ToLower(s)
	return strings.Contains(sl, "code review") || strings.Contains(sl, "in review") ||
		strings.Contains(sl, "peer review")
}

func isReworkTransition(from, to string) bool {
	fromLow, toLow := strings.ToLower(from), strings.ToLower(to)
	laterStatus := strings.Contains(fromLow, "review") || strings.Contains(fromLow, "test") ||
		strings.Contains(fromLow, "qa") || strings.Contains(fromLow, "done") ||
		strings.Contains(fromLow, "closed") || strings.Contains(fromLow, "resolv") ||
		strings.Contains(fromLow, "accept")
	reworkTarget := toLow == "in progress" || strings.Contains(toLow, "fix") ||
		strings.Contains(toLow, "rework") || strings.Contains(toLow, "revise") ||
		strings.Contains(toLow, "reopen")
	return laterStatus && reworkTarget
}

func memberStatsFromReports(displayName string, reports []domain.SprintReportData) []domain.MemberSprintStats {
	stats := make([]domain.MemberSprintStats, 0, len(reports))
	for _, r := range reports {
		var completedSP, incompleteSP float64
		var completedCount, incompleteCount int
		for _, issue := range r.Completed {
			if issue.Assignee == displayName {
				if issue.FinalSP != nil {
					completedSP += *issue.FinalSP
				}
				completedCount++
			}
		}
		for _, issue := range r.NotCompleted {
			if issue.Assignee == displayName {
				if issue.FinalSP != nil {
					incompleteSP += *issue.FinalSP
				}
				incompleteCount++
			}
		}
		stats = append(stats, domain.MemberSprintStats{
			Sprint:          r.Sprint,
			CompletedSP:     completedSP,
			IncompleteSP:    incompleteSP,
			CompletedCount:  completedCount,
			IncompleteCount: incompleteCount,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Sprint.EndDate < stats[j].Sprint.EndDate
	})
	return stats
}

// spTrend splits stats into two halves and returns signals.
func spTrend(stats []domain.MemberSprintStats) (recentAvg, historicalAvg float64, signal string) {
	closed := make([]domain.MemberSprintStats, 0)
	for _, s := range stats {
		if strings.ToLower(s.Sprint.State) != "active" {
			closed = append(closed, s)
		}
	}
	if len(closed) < 2 {
		return 0, 0, "stable"
	}
	half := len(closed) / 2
	var recentSum, histSum float64
	for _, s := range closed[half:] {
		recentSum += s.CompletedSP
	}
	for _, s := range closed[:half] {
		histSum += s.CompletedSP
	}
	recentCount := float64(len(closed) - half)
	histCount := float64(half)
	recentAvg = recentSum / recentCount
	historicalAvg = histSum / histCount
	if historicalAvg == 0 {
		return recentAvg, historicalAvg, "stable"
	}
	ratio := recentAvg / historicalAvg
	if ratio < 0.75 {
		return recentAvg, historicalAvg, "down"
	}
	if ratio > 1.25 {
		return recentAvg, historicalAvg, "up"
	}
	return recentAvg, historicalAvg, "stable"
}

// commitsByWeek groups commits into ISO-week buckets, returns weeks sorted desc.
func commitsByWeek(commits []domain.GitCommit) []struct {
	Label string
	Count int
} {
	counts := map[string]int{}
	for _, c := range commits {
		t, err := time.Parse("2006-01-02", c.Date)
		if err != nil {
			continue
		}
		y, w := t.ISOWeek()
		key := fmt.Sprintf("%d-W%02d", y, w)
		counts[key]++
	}
	type week struct {
		Label string
		Count int
	}
	weeks := make([]week, 0, len(counts))
	for k, v := range counts {
		weeks = append(weeks, week{k, v})
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Label > weeks[j].Label })
	result := make([]struct {
		Label string
		Count int
	}, len(weeks))
	for i, w := range weeks {
		result[i].Label = w.Label
		result[i].Count = w.Count
	}
	return result
}

func spBar(val, max float64, width int) string {
	if max == 0 || val == 0 {
		return ""
	}
	blocks := int(math.Round(val / max * float64(width)))
	if blocks < 1 {
		blocks = 1
	}
	return strings.Repeat("■", blocks)
}

func inProgressCount(issues []domain.JiraIssue) int {
	n := 0
	for _, i := range issues {
		if i.StatusCategory == "In Progress" {
			n++
		}
	}
	return n
}

func workloadSignal(openCount, inProgress int, recentAvgSP float64) string {
	if inProgress > 5 {
		return "very-high"
	}
	if inProgress > 3 {
		return "high"
	}
	_ = recentAvgSP // future: compare open SP vs velocity
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Renderer ---

func (m OneOnOneModel) renderContent() string {
	var sb strings.Builder
	bold := lipgloss.NewStyle().Bold(true)

	stats := memberStatsFromReports(m.member.DisplayName, m.sprintReports)
	recentAvg, histAvg, trend := spTrend(stats)
	weeks := commitsByWeek(m.commits)
	inProg := inProgressCount(m.openIssues)
	wlSignal := workloadSignal(len(m.openIssues), inProg, recentAvg)

	// Sprint Velocity section
	sb.WriteString(bold.Render("Sprint Velocity") + "  (last 6 sprints)\n")
	if len(stats) == 0 {
		sb.WriteString(dimStyle.Render("  No sprint data available.\n"))
	} else {
		maxSP := 0.0
		for _, s := range stats {
			if s.CompletedSP > maxSP {
				maxSP = s.CompletedSP
			}
		}
		// Render most-recent first
		for i := len(stats) - 1; i >= 0; i-- {
			s := stats[i]
			bar := spBar(s.CompletedSP, maxSP, 12)
			stateTag := ""
			if strings.ToLower(s.Sprint.State) == "active" {
				stateTag = dimStyle.Render(" [active]")
			}
			line := fmt.Sprintf("  %-26s %-13s%5.0f SP  (%d done)%s\n",
				truncateName(s.Sprint.Name, 26), bar, s.CompletedSP, s.CompletedCount, stateTag)
			sb.WriteString(line)
		}
		sb.WriteString("\n")
		trendLine := fmt.Sprintf("  Avg: %.0f SP/sprint   Recent avg: %.0f", histAvg, recentAvg)
		switch trend {
		case "down":
			sb.WriteString(trendLine + "   " + warnStyle.Render("↓ lower than usual") + "\n\n")
			sb.WriteString(warnStyle.Render("  ⚠  Velocity below average the last few sprints") + "\n")
		case "up":
			sb.WriteString(trendLine + "   " + lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("↑ higher than usual") + "\n")
		default:
			sb.WriteString(trendLine + "   steady\n")
		}
	}
	sb.WriteString("\n")

	// Open Issues section
	baseURL := strings.TrimRight(m.cfg.Jira.BaseURL, "/")
	sb.WriteString(bold.Render(fmt.Sprintf("Open Issues  (%d)", len(m.openIssues))) + "\n")
	if len(m.openIssues) == 0 {
		sb.WriteString(dimStyle.Render("  None — all clear.\n"))
	} else {
		for _, issue := range m.openIssues {
			cat := statusCatLabel(issue.StatusCategory)
			keyLink := jiraLink(issue.Key, baseURL+"/browse/"+issue.Key)
			summary := truncateName(issue.Summary, 60)
			sb.WriteString(fmt.Sprintf("  %-13s  %-12s  %s\n", cat, keyLink, summary))
		}
		if wlSignal == "very-high" {
			sb.WriteString("\n" + warnStyle.Render("  ⚠  High number of in-progress items") + "\n")
		} else if wlSignal == "high" {
			sb.WriteString("\n" + warnStyle.Render("  ⚠  More in-flight than usual") + "\n")
		}
	}
	sb.WriteString("\n")

	// Git Activity section
	gitFilter := m.member.Email
	if gitFilter == "" {
		gitFilter = m.member.DisplayName
	}
	sb.WriteString(bold.Render("Git Activity") + "  (last 8 weeks)\n")
	if len(m.team.Repos) == 0 {
		sb.WriteString(dimStyle.Render("  No repositories configured for this team.\n"))
	} else if len(weeks) == 0 {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  No commits found.  (filter: --author=%s)\n", gitFilter)))
	} else {
		maxCommits := 0
		for _, w := range weeks {
			if w.Count > maxCommits {
				maxCommits = w.Count
			}
		}
		limit := 8
		if len(weeks) < limit {
			limit = len(weeks)
		}
		var totalCommits int
		for _, w := range weeks {
			totalCommits += w.Count
		}
		avgCommits := 0
		if len(weeks) > 0 {
			avgCommits = totalCommits / len(weeks)
		}
		for _, w := range weeks[:limit] {
			bar := strings.Repeat("█", int(math.Round(float64(w.Count)/float64(maxCommits)*10)))
			sb.WriteString(fmt.Sprintf("  %-11s  %-11s %d commits\n", w.Label, bar, w.Count))
		}
		sb.WriteString("\n")
		thisWeek := 0
		if len(weeks) > 0 {
			thisWeek = weeks[0].Count
		}
		weekLine := fmt.Sprintf("  Avg: %d commits/week   This week: %d", avgCommits, thisWeek)
		if avgCommits > 0 && thisWeek < avgCommits/2 {
			sb.WriteString(weekLine + "  " + warnStyle.Render("(below average)") + "\n")
		} else {
			sb.WriteString(weekLine + "\n")
		}
	}
	sb.WriteString("\n")

	// DORA Metrics section
	d := m.doraMetrics
	if d.IssuesAnalyzed > 0 {
		sb.WriteString(bold.Render(fmt.Sprintf("DORA Metrics  (90 days, %d issues)", d.IssuesAnalyzed)) + "\n")
		if d.AvgCycleTimeDays > 0 {
			sb.WriteString(fmt.Sprintf("  Cycle time:        %.1f days\n", d.AvgCycleTimeDays))
		}
		if d.AvgCodeReviewDays > 0 {
			sb.WriteString(fmt.Sprintf("  Code review wait:  %.1f days\n", d.AvgCodeReviewDays))
		}
		reworkLine := fmt.Sprintf("  Rework incidents:  %d issues", d.ReworkCount)
		if d.ReworkCount > 0 {
			sb.WriteString(reworkLine + "  " + warnStyle.Render("⚠ worth discussing") + "\n")
		} else {
			sb.WriteString(reworkLine + "\n")
		}
		sb.WriteString("\n")
	}

	// Talking Points
	sb.WriteString(bold.Render("Talking Points") + "\n")
	var points []string
	if trend == "down" {
		points = append(points, "Velocity has been below average for the last few sprints. Worth exploring blockers.")
	}
	if wlSignal != "" {
		points = append(points, "More in-progress items than usual — watch for overload signals.")
	}
	if len(weeks) > 1 && weeks[0].Count < avgCommitsPerWeek(weeks)/2 {
		points = append(points, "Lighter commit activity this week than recent average.")
	}
	if d.ReworkCount > 0 {
		points = append(points, fmt.Sprintf("%d issue(s) required rework (status regressed after code review/testing).", d.ReworkCount))
	}
	if d.AvgCodeReviewDays > 2 {
		points = append(points, fmt.Sprintf("Code review is averaging %.1f days — consider pairing or smaller PRs.", d.AvgCodeReviewDays))
	}
	if len(points) == 0 {
		points = append(points, "No workload or velocity concerns this cycle.")
	}
	for _, p := range points {
		sb.WriteString("  • " + p + "\n")
	}

	return sb.String()
}

func avgCommitsPerWeek(weeks []struct {
	Label string
	Count int
}) int {
	if len(weeks) == 0 {
		return 0
	}
	total := 0
	for _, w := range weeks {
		total += w.Count
	}
	return total / len(weeks)
}

func truncateName(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func statusCatLabel(cat string) string {
	switch cat {
	case "In Progress":
		return "In Progress"
	case "Done":
		return "Done"
	default:
		return "To Do"
	}
}

// jiraLink renders an OSC 8 hyperlink for terminals that support it (iTerm2, Kitty, WezTerm, Ghostty).
// Falls back gracefully — unsupported terminals ignore the escape sequences.
func jiraLink(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
