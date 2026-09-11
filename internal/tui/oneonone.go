package tui

import (
	"fmt"
	"math"
	"os/exec"
	"runtime"
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
	ooStateDetail // drill-down table for one talking point
)

// ooFocus is which part of the report the keyboard is driving. j/k means scroll
// by default; tab hands it to the talking-point list so the same keys select.
type ooFocus int

const (
	ooFocusScroll ooFocus = iota
	ooFocusPoints
)

// ticketRef is one row in a talking-point drill-down.
type ticketRef struct {
	Key     string
	Summary string
	Status  string
	Detail  string // the metric that put it on this list, e.g. "2 bounces", "27d"
}

// talkingPoint is one generated observation. Tickets is nil for points with no
// specific issues behind them (velocity trends, commit cadence), and those are
// skipped when selecting, so the cursor only ever lands on something openable.
type talkingPoint struct {
	Lines      []string
	Title      string
	DetailHead string // column header for the Detail field
	Tickets    []ticketRef
}

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
	categoriesLoaded    bool

	// catByID maps Jira status ID to status category name. Keyed by ID because
	// status names are not unique across a site.
	catByID             map[string]string
	flow                domain.MemberFlow
	openAges            []domain.OpenIssueAge
	changelogWindowFrom time.Time

	// Computed once when loading finishes, not per keystroke: the report
	// re-renders on every selection move.
	points    []talkingPoint
	drillable []int // indices into points that have tickets behind them

	focus     ooFocus
	selected  int // index into drillable
	detailIdx int // index into points, for the open detail view
	detailRow int

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
		m.pendingReports > 0 || !m.changelogsLoaded || !m.categoriesLoaded
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
		m.jiraClient.FetchStatusCategories(),
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
			// Don't short-circuit changelogs here: the member's open issues may
			// still yield keys worth fetching histories for.
			m.pendingReports = 0
			if cmd := m.maybeFetchChangelogs(); cmd != nil {
				return m, cmd
			}
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
		if cmd := m.maybeFetchChangelogs(); cmd != nil {
			return m, cmd
		}
		m.finishIfDone()
		return m, nil

	case jira.StatusCategoriesResult:
		if msg.Err == nil {
			m.catByID = msg.ByID
		}
		m.categoriesLoaded = true
		m.finishIfDone()
		return m, nil

	case jira.ChangelogsResult:
		if msg.AccountID == m.member.AccountID {
			// Open issues are guaranteed present: maybeFetchChangelogs gates on
			// issuesLoaded. So both derived values compute here off one clock.
			now := time.Now()
			byKey := make(map[string]domain.IssueChangelog, len(msg.Changelogs))
			for _, cl := range msg.Changelogs {
				byKey[cl.Key] = cl
			}
			openKeys := make(map[string]bool, len(m.openIssues))
			for _, iss := range m.openIssues {
				openKeys[iss.Key] = true
			}
			m.flow = m.fc().computeFlow(msg.Changelogs, openKeys, m.changelogWindowFrom, now)
			m.openAges = openIssueAges(m.openIssues, byKey, now)
		}
		m.changelogsLoaded = true
		m.finishIfDone()
		return m, nil

	case jira.IssueResult:
		if msg.AccountID == m.member.AccountID && msg.Err == nil {
			m.openIssues = msg.Issues
		}
		m.issuesLoaded = true
		if cmd := m.maybeFetchChangelogs(); cmd != nil {
			return m, cmd
		}
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
		return m.handleKey(msg)
	}
	return m, nil
}

func (m OneOnOneModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Detail view has its own key map; esc backs out to the report rather than
	// leaving the screen entirely.
	if m.state == ooStateDetail {
		rows := 0
		if m.detailIdx < len(m.points) {
			rows = len(m.points[m.detailIdx].Tickets)
		}
		switch key {
		case "esc", "q", "left", "h":
			m.state = ooStateReport
			m.viewport.SetContent(m.renderContent())
			return m, nil
		case "j", "down":
			if m.detailRow < rows-1 {
				m.detailRow++
			}
			return m, nil
		case "k", "up":
			if m.detailRow > 0 {
				m.detailRow--
			}
			return m, nil
		case "g", "home":
			m.detailRow = 0
			return m, nil
		case "G", "end":
			m.detailRow = max(0, rows-1)
			return m, nil
		case "enter", "o":
			if m.detailRow < rows {
				url := strings.TrimRight(m.cfg.Jira.BaseURL, "/") + "/browse/" +
					m.points[m.detailIdx].Tickets[m.detailRow].Key
				return m, openInBrowser(url)
			}
			return m, nil
		}
		return m, nil
	}

	switch key {
	case "q":
		return m, func() tea.Msg { return GoBack{} }

	case "esc":
		// Esc first drops focus back to scrolling, so it takes two presses to
		// leave the screen from selection mode rather than one surprising one.
		if m.focus == ooFocusPoints {
			m.focus = ooFocusScroll
			m.viewport.SetContent(m.renderContent())
			return m, nil
		}
		return m, func() tea.Msg { return GoBack{} }

	case "tab", "shift+tab":
		if m.state != ooStateReport || len(m.drillable) == 0 {
			return m, nil
		}
		if m.focus == ooFocusPoints {
			m.focus = ooFocusScroll
		} else {
			m.focus = ooFocusPoints
			m.selected = 0
			m.viewport.GotoTop() // the points are at the top; show what's selected
		}
		m.viewport.SetContent(m.renderContent())
		return m, nil
	}

	if m.focus == ooFocusPoints && m.state == ooStateReport {
		switch key {
		case "j", "down":
			if m.selected < len(m.drillable)-1 {
				m.selected++
			}
			m.viewport.SetContent(m.renderContent())
			return m, nil
		case "k", "up":
			if m.selected > 0 {
				m.selected--
			}
			m.viewport.SetContent(m.renderContent())
			return m, nil
		case "enter", "l", "right":
			if m.selected < len(m.drillable) {
				m.detailIdx = m.drillable[m.selected]
				m.detailRow = 0
				m.state = ooStateDetail
			}
			return m, nil
		}
	}

	if m.state == ooStateReport {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

// openInBrowser launches the platform URL handler. Errors are swallowed: a
// failure here should never take down the TUI, and the key is also an OSC 8
// hyperlink the user can click.
func openInBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
		return nil
	}
}

// finishIfDone transitions to ooStateReport once all data is loaded.
// Must be called on a pointer to m (captured via &m in Update's value receiver).
func (m *OneOnOneModel) finishIfDone() {
	if m.loading() || m.state != ooStateLoading {
		return
	}
	m.state = ooStateReport

	now := time.Now()
	stats := memberStatsFromReports(m.member.DisplayName, m.sprintReports)
	recentAvg, histAvg, trend := spTrend(stats)
	weeks := commitsByWeek(m.commits, now, 8)
	avg, thisWeek, active, _ := commitCadence(weeks)
	carry := carryoverStats(m.member.DisplayName, stats, m.sprintReports)
	m.points = m.buildTalkingPoints(stats, trend, recentAvg, histAvg, carry,
		belowAverageCommits(weeks, avg, thisWeek, active, now))

	m.drillable = m.drillable[:0]
	for i, p := range m.points {
		if len(p.Tickets) > 0 {
			m.drillable = append(m.drillable, i)
		}
	}

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
	case ooStateDetail:
		body := m.renderDetail()
		// Pad to the viewport height so the footer doesn't jump up the screen on
		// a short table.
		if n := m.viewport.Height - strings.Count(body, "\n"); n > 0 {
			body += strings.Repeat("\n", n)
		}
		return header + rule + body +
			"\n" + dimStyle.Render("j/k select   enter open in browser   esc back to report")
	}

	if !m.ready {
		return header + rule + "\n" + m.renderContent()
	}
	hints := "j/k scroll   q back"
	if m.focus == ooFocusPoints {
		hints = "j/k select point   enter open   tab back to scroll   esc cancel"
	} else if len(m.drillable) > 0 {
		hints = "j/k scroll   tab select talking point   q back"
	}
	return header + rule + m.viewport.View() + "\n" + dimStyle.Render(hints)
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

// ooMaxChangelogIssues caps how many issue histories we fetch per person. Each
// key is one HTTP GET at concurrency 10, so 80 is roughly 8 round trips.
const ooMaxChangelogIssues = 80

// maybeFetchChangelogs fires the single changelog batch once BOTH the sprint
// reports and the member's open issues have landed, since the key list needs
// both. Safe to call from any branch: it no-ops until every precondition holds
// and never fires twice. When there is nothing to fetch it marks changelogs
// loaded itself, so the loading() gate always clears.
func (m *OneOnOneModel) maybeFetchChangelogs() tea.Cmd {
	if m.changelogsRequested || m.state == ooStateError {
		return nil
	}
	if !m.sprintsLoaded || m.pendingReports > 0 || !m.issuesLoaded {
		return nil
	}
	m.changelogsRequested = true
	keys, from := m.issueKeysForChangelog()
	if from.IsZero() {
		from = time.Now().AddDate(0, 0, -90)
	}
	m.changelogWindowFrom = from
	if len(keys) == 0 {
		m.changelogsLoaded = true
		return nil
	}
	return m.jiraClient.FetchChangelogs(m.member.AccountID, keys)
}

// issueKeysForChangelog collects the keys we need histories for: this member's
// currently-open issues, plus everything they completed in the last 90 days. It
// also returns the start of the window actually sampled, which the throughput
// denominator uses — a hardcoded 90 days would overstate the window whenever the
// cap truncates the sample.
func (m *OneOnOneModel) issueKeysForChangelog() ([]string, time.Time) {
	cutoff := time.Now().AddDate(0, -3, 0)
	seen := map[string]bool{}
	var keys []string
	var from time.Time

	// Open issues first so the cap can never evict them; they're the ones we need
	// for time-in-status.
	for _, iss := range m.openIssues {
		if !seen[iss.Key] {
			seen[iss.Key] = true
			keys = append(keys, iss.Key)
		}
	}

	// sprintReports is appended in async completion order, so iterating it as-is
	// makes the cap drop a random set of sprints run to run. Sort newest-first so
	// truncation deterministically drops the oldest.
	reports := append([]domain.SprintReportData(nil), m.sprintReports...)
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].Sprint.EndDate > reports[j].Sprint.EndDate
	})

	for _, r := range reports {
		endDate, err := time.Parse("2006-01-02", r.Sprint.EndDate)
		if err != nil || endDate.Before(cutoff) {
			continue
		}
		if start, err := time.Parse("2006-01-02", r.Sprint.StartDate); err == nil {
			if from.IsZero() || start.Before(from) {
				from = start
			}
		}
		for _, issue := range r.Completed {
			if issue.Assignee == m.member.DisplayName && !seen[issue.Key] {
				seen[issue.Key] = true
				keys = append(keys, issue.Key)
				if len(keys) >= ooMaxChangelogIssues {
					return keys, from
				}
			}
		}
	}
	return keys, from
}

// category returns the Jira status category for a transition endpoint. The ID is
// authoritative; the name is only a fallback for the rare transition where Jira
// omits the ID (some creation paths) or the status map failed to load.
// fc builds a flowComputer from the model's loaded status-category map and the
// team's effective phase table. Both are set during loading and do not change
// after that, so it is safe to call from Update or any render helper.
func (m OneOnOneModel) fc() flowComputer {
	return flowComputer{catByID: m.catByID, phases: m.team.EffectivePhases()}
}

// guessCategory is the keyword fallback for when the status map is unavailable.
// Deliberately conservative: it is better to return To Do and have the phase
// land in Unclassified than to guess Done and truncate a cycle time.
func guessCategory(name string) string {
	sl := strings.ToLower(strings.TrimSpace(name))
	switch sl {
	case "done", "closed", "resolved", "deployed", "released", "complete", "completed":
		return domain.CatDone
	case "to do", "todo", "open", "new", "backlog":
		return domain.CatTodo
	}
	if strings.Contains(sl, "deploy") || strings.Contains(sl, "closed") {
		return domain.CatDone
	}
	if strings.Contains(sl, "backlog") || strings.Contains(sl, "triage") {
		return domain.CatTodo
	}
	return domain.CatInProgress
}

// phaseByKeyword is the fallback for In Progress statuses absent from the phase
// table. "Ready for X" and "Awaiting X" resolve to the queue phase rather than to
// X itself, which is the distinction the whole breakdown rests on.
func phaseByKeyword(name string) domain.Phase {
	sl := strings.ToLower(name)
	queued := strings.Contains(sl, "ready for") || strings.Contains(sl, "ready to") ||
		strings.Contains(sl, "awaiting") || strings.Contains(sl, "waiting")
	switch {
	case strings.Contains(sl, "block") || strings.Contains(sl, "on hold") || strings.Contains(sl, "parked"):
		return domain.PhaseBlocked
	case strings.Contains(sl, "fix needed") || strings.Contains(sl, "rework") || strings.Contains(sl, "reopen"):
		return domain.PhaseRework
	case strings.Contains(sl, "qa") || strings.Contains(sl, "test") || strings.Contains(sl, "uat"):
		if queued {
			return domain.PhaseAwaitingQA
		}
		return domain.PhaseQA
	case strings.Contains(sl, "review") || strings.Contains(sl, "merge"):
		if queued {
			return domain.PhaseAwaitingMerge
		}
		return domain.PhaseReview
	case strings.Contains(sl, "progress") || strings.Contains(sl, "development") || strings.Contains(sl, "started"):
		return domain.PhaseDev
	}
	return domain.PhaseUnknown
}


// openIssueAges derives how long each open issue has sat in its present status.
// Falls back to the issue's Updated date, flagged inexact, when the changelog is
// missing or disagrees with the live status.
func openIssueAges(issues []domain.JiraIssue, byKey map[string]domain.IssueChangelog,
	now time.Time) []domain.OpenIssueAge {

	out := make([]domain.OpenIssueAge, 0, len(issues))
	for _, iss := range issues {
		age := domain.OpenIssueAge{Key: iss.Key}
		cl, ok := byKey[iss.Key]
		switch {
		case ok && len(cl.Transitions) > 0 &&
			strings.EqualFold(cl.Transitions[len(cl.Transitions)-1].To, iss.Status):
			age.DaysInStatus = now.Sub(cl.Transitions[len(cl.Transitions)-1].Timestamp).Hours() / 24
			age.Exact = true
		case ok && len(cl.Transitions) == 0 && !cl.Created.IsZero():
			// Never moved since creation.
			age.DaysInStatus = now.Sub(cl.Created).Hours() / 24
			age.Exact = true
		default:
			if t, err := domain.ParseJiraTime(iss.Updated); err == nil {
				age.DaysInStatus = now.Sub(t).Hours() / 24
			}
		}
		out = append(out, age)
	}
	return out
}

// carryoverStats derives what fraction of a member's committed points roll to the
// next sprint, plus their median completed ticket size.
func carryoverStats(displayName string, stats []domain.MemberSprintStats,
	reports []domain.SprintReportData) domain.CarryoverStats {

	var cs domain.CarryoverStats
	var sumInc, sumTotal float64
	for _, s := range stats {
		if strings.ToLower(s.Sprint.State) == "active" {
			continue // carryover isn't decided until the sprint closes
		}
		total := s.CompletedSP + s.IncompleteSP
		if total <= 0 {
			// Unestimated sprints sum to 0/0. Counting them as 0% carryover would
			// dilute the rate toward optimism, so skip them.
			continue
		}
		cs.SprintsUsed++
		sumInc += s.IncompleteSP
		sumTotal += total
	}
	if sumTotal > 0 {
		// Pooled rather than a mean of per-sprint ratios, which would weight a
		// 2-point sprint the same as a 20-point one.
		cs.PooledRate = sumInc / sumTotal
	}

	seen := map[string]bool{}
	var sizes []float64
	for _, r := range reports {
		for _, iss := range r.Completed {
			// FinalSP is nil for unestimated tickets; including them as 0 would
			// drag the median to 0 on an unestimated-heavy sprint.
			if iss.Assignee != displayName || iss.FinalSP == nil || seen[iss.Key] {
				continue
			}
			seen[iss.Key] = true
			sizes = append(sizes, *iss.FinalSP)
		}
	}
	sort.Float64s(sizes)
	cs.TicketsSized = len(sizes)
	cs.MedianTicketSP = percentile(sizes, 50)
	return cs
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

// isoWeekStart returns midnight UTC on the Monday of t's ISO week. Note that
// time.Truncate(24*time.Hour) is NOT a substitute: it truncates against the UTC
// epoch and shifts local dates by a day.
func isoWeekStart(t time.Time) time.Time {
	y, mo, d := t.Date()
	day := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	wd := int(day.Weekday())
	if wd == 0 {
		wd = 7 // Sunday
	}
	return day.AddDate(0, 0, 1-wd)
}

// commitsByWeek buckets commits into a fixed-length window of ISO weeks, most
// recent first. Weeks with no commits are present in the result, which is the
// whole point: averaging over only the weeks that had commits reports "average
// per active week" and overstates the real cadence.
func commitsByWeek(commits []domain.GitCommit, now time.Time, weeks int) []domain.WeekCount {
	if weeks < 1 {
		weeks = 1
	}
	current := isoWeekStart(now)
	buckets := make([]domain.WeekCount, weeks)
	index := make(map[time.Time]int, weeks)
	for i := range buckets {
		start := current.AddDate(0, 0, -7*i)
		y, w := start.ISOWeek()
		buckets[i] = domain.WeekCount{Label: fmt.Sprintf("%d-W%02d", y, w)}
		index[start] = i
	}
	for _, c := range commits {
		t, err := time.Parse("2006-01-02", c.Date)
		if err != nil {
			continue
		}
		if i, ok := index[isoWeekStart(t)]; ok {
			buckets[i].Count++
		}
	}
	return buckets
}

// commitCadence summarises the week buckets. avg is a float over the full window,
// not integer-divided over active weeks as the previous version was.
func commitCadence(weeks []domain.WeekCount) (avg float64, thisWeek, active, total int) {
	for _, w := range weeks {
		total += w.Count
		if w.Count > 0 {
			active++
		}
	}
	if len(weeks) > 0 {
		avg = float64(total) / float64(len(weeks))
		thisWeek = weeks[0].Count
	}
	return avg, thisWeek, active, total
}

// belowAverageCommits reports whether this week's commit count is meaningfully
// low. The current week is partial, so the average is pro-rated by days elapsed;
// comparing a Tuesday against a full-week average would fire almost every week.
func belowAverageCommits(weeks []domain.WeekCount, avg float64, thisWeek, active int, now time.Time) bool {
	if len(weeks) < 4 || active < 3 || avg < 2 {
		return false // too little history, or too low a volume, to judge
	}
	elapsed := now.Sub(isoWeekStart(now)).Hours()/24 + 1
	if elapsed > 7 {
		elapsed = 7
	}
	return float64(thisWeek) < (avg*elapsed/7)/2
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

// plural returns the "s" suffix for count, so generated talking points read
// naturally when spoken aloud in a 1:1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// fmtDays renders a day count compactly: "6h", "1.4d", "12d".
func fmtDays(d float64) string {
	switch {
	case d <= 0:
		return "0"
	case d < 1:
		return fmt.Sprintf("%.0fh", d*24)
	case d < 10:
		return fmt.Sprintf("%.1fd", d)
	default:
		return fmt.Sprintf("%.0fd", d)
	}
}

// --- Renderer ---

func (m OneOnOneModel) renderContent() string {
	var sb strings.Builder
	now := time.Now()

	// Everything is computed up front. Talking Points renders first but depends on
	// every section below it, so nothing may compute inline during rendering.
	stats := memberStatsFromReports(m.member.DisplayName, m.sprintReports)
	recentAvg, histAvg, trend := spTrend(stats)
	weeks := commitsByWeek(m.commits, now, 8)
	avgCommits, thisWeek, activeWeeks, totalCommits := commitCadence(weeks)
	commitsLow := belowAverageCommits(weeks, avgCommits, thisWeek, activeWeeks, now)
	carry := carryoverStats(m.member.DisplayName, stats, m.sprintReports)

	sb.WriteString(m.renderTalkingPoints())
	sb.WriteString(m.renderCycleTime(carry))
	sb.WriteString(m.renderVelocity(stats, recentAvg, histAvg, trend))
	sb.WriteString(m.renderOpenIssues())
	sb.WriteString(m.renderGitActivity(weeks, avgCommits, thisWeek, activeWeeks, totalCommits, commitsLow))
	sb.WriteString(m.renderMergeRequests())
	return sb.String()
}

// ticketInfo builds a key → summary/status lookup from data already fetched:
// open issues carry both, and the sprint reports cover completed ones.
func (m OneOnOneModel) ticketInfo() map[string]ticketRef {
	info := map[string]ticketRef{}
	for _, r := range m.sprintReports {
		for _, group := range [][]domain.SprintIssueCompact{r.Completed, r.NotCompleted, r.Punted} {
			for _, iss := range group {
				if _, seen := info[iss.Key]; !seen {
					info[iss.Key] = ticketRef{Key: iss.Key, Summary: iss.Summary, Status: iss.Status}
				}
			}
		}
	}
	// Open issues win: they are the more current of the two.
	for _, iss := range m.openIssues {
		info[iss.Key] = ticketRef{Key: iss.Key, Summary: iss.Summary, Status: iss.Status}
	}
	return info
}

// refsFor turns issue keys into drill-down rows, attaching the metric that put
// each one on the list. Keys with no known summary still render, since a bare
// key is more useful during a 1:1 than a silently dropped row.
func (m OneOnOneModel) refsFor(keys []string, detail func(string) string) []ticketRef {
	info := m.ticketInfo()
	out := make([]ticketRef, 0, len(keys))
	for _, k := range keys {
		r, ok := info[k]
		if !ok {
			r = ticketRef{Key: k, Summary: dimStyle.Render("(not in the sampled sprints)")}
		}
		if detail != nil {
			r.Detail = detail(k)
		}
		out = append(out, r)
	}
	return out
}

// buildTalkingPoints generates the observations and the tickets behind each one.
// Called once when loading finishes: the report re-renders on every selection
// move, so this must not run per keystroke.
func (m OneOnOneModel) buildTalkingPoints(stats []domain.MemberSprintStats, trend string,
	recentAvg, histAvg float64, carry domain.CarryoverStats, commitsLow bool) []talkingPoint {

	f := m.flow
	var pts []talkingPoint

	byKey := map[string]domain.IssueCycle{}
	for _, c := range f.Issues {
		byKey[c.Key] = c
	}

	// Lead with the phase breakdown when one non-dev phase dominates: that is the
	// difference between a coding problem and a queue problem.
	if top, share := topPhase(f.PhaseShare); share > 0.4 && top != domain.PhaseDev && top != domain.PhaseTodo {
		tail := "Worth asking what's slow there."
		if top.IsWait() {
			tail = "Queue problem, not a coding problem."
		}
		// The worst offenders in that phase, so "which ones?" has an answer.
		var keys []string
		for _, c := range f.Issues {
			if c.Phases[top] > 0 {
				keys = append(keys, c.Key)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return byKey[keys[i]].Phases[top] > byKey[keys[j]].Phases[top] })
		if len(keys) > 15 {
			keys = keys[:15]
		}
		pts = append(pts, talkingPoint{
			Lines: []string{
				fmt.Sprintf("%.0f%% of cycle time is spent in %s (%s avg).", share*100, top, fmtDays(f.AvgPhase[top])),
				tail,
			},
			Title:      fmt.Sprintf("Longest time in %s", top),
			DetailHead: "IN PHASE",
			Tickets:    m.refsFor(keys, func(k string) string { return fmtDays(byKey[k].Phases[top]) }),
		})
	}

	if f.IssuesCompleted >= 8 && f.MaxCycleDays > 3*f.P50CycleDays && f.MaxCycleKey != "" {
		var keys []string
		for _, c := range f.Issues {
			if c.CycleDays > 2*f.P50CycleDays {
				keys = append(keys, c.Key)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return byKey[keys[i]].CycleDays > byKey[keys[j]].CycleDays })
		pts = append(pts, talkingPoint{
			Lines: []string{fmt.Sprintf("%s took %s against a median of %s — worth asking what stalled.",
				f.MaxCycleKey, fmtDays(f.MaxCycleDays), fmtDays(f.P50CycleDays))},
			Title:      "Slowest tickets",
			DetailHead: "CYCLE",
			Tickets:    m.refsFor(keys, func(k string) string { return fmtDays(byKey[k].CycleDays) }),
		})
	}

	if f.QABounceIssues > 0 {
		var keys []string
		for _, c := range f.Issues {
			if c.QABounces > 0 {
				keys = append(keys, c.Key)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return byKey[keys[i]].QABounces > byKey[keys[j]].QABounces })
		pts = append(pts, talkingPoint{
			Lines: []string{fmt.Sprintf("%d issue%s kicked back from QA or review (%d times).",
				f.QABounceIssues, plural(f.QABounceIssues), f.QABounceEvents)},
			Title:      "Kicked back from QA or review",
			DetailHead: "BOUNCES",
			Tickets:    m.refsFor(keys, func(k string) string { return fmt.Sprintf("%d", byKey[k].QABounces) }),
		})
	}

	if f.ReopenedIssues > 0 {
		var keys []string
		for _, c := range f.Issues {
			if c.Reopened > 0 {
				keys = append(keys, c.Key)
			}
		}
		pts = append(pts, talkingPoint{
			Lines:      []string{fmt.Sprintf("%d issue%s reopened after being marked done.", f.ReopenedIssues, plural(f.ReopenedIssues))},
			Title:      "Reopened after done",
			DetailHead: "REOPENS",
			Tickets:    m.refsFor(keys, func(k string) string { return fmt.Sprintf("%d", byKey[k].Reopened) }),
		})
	}

	if stale := staleOpenIssues(m.openAges, 14); len(stale) > 0 {
		sort.Slice(stale, func(i, j int) bool { return stale[i].DaysInStatus > stale[j].DaysInStatus })
		ageByKey := map[string]float64{}
		keys := make([]string, 0, len(stale))
		for _, a := range stale {
			ageByKey[a.Key] = a.DaysInStatus
			keys = append(keys, a.Key)
		}
		pts = append(pts, talkingPoint{
			Lines:      []string{fmt.Sprintf("%d open issue%s sat in the same status over 14 days.", len(stale), plural(len(stale)))},
			Title:      "Stale open issues",
			DetailHead: "IN STATUS",
			Tickets:    m.refsFor(keys, func(k string) string { return fmtDays(ageByKey[k]) }),
		})
	}

	if inProg := inProgressCount(m.openIssues); inProg > 5 {
		var keys []string
		for _, iss := range m.openIssues {
			if iss.StatusCategory == "In Progress" {
				keys = append(keys, iss.Key)
			}
		}
		pts = append(pts, talkingPoint{
			Lines:      []string{fmt.Sprintf("%d items in progress at once — watch for context switching.", inProg)},
			Title:      "Currently in progress",
			DetailHead: "",
			Tickets:    m.refsFor(keys, nil),
		})
	}

	// Points with no specific tickets behind them. They still render, they just
	// aren't selectable.
	if trend == "down" {
		pts = append(pts, talkingPoint{Lines: []string{fmt.Sprintf(
			"Velocity is below average lately (%.0f SP vs %.0f). Worth exploring blockers.", recentAvg, histAvg)}})
	}
	if carry.PooledRate > 0.3 {
		pts = append(pts, talkingPoint{Lines: []string{fmt.Sprintf(
			"Carrying over %.0f%% of committed points across %d sprint%s.", carry.PooledRate*100, carry.SprintsUsed, plural(carry.SprintsUsed))}})
	}
	if commitsLow {
		pts = append(pts, talkingPoint{Lines: []string{"Lighter commit activity this week than the recent average."}})
	}
	if len(pts) == 0 {
		pts = append(pts, talkingPoint{Lines: []string{"No workload, velocity or flow concerns this cycle."}})
	}
	return pts
}

func (m OneOnOneModel) renderTalkingPoints() string {
	var sb strings.Builder
	head := boldStyle.Render("Talking Points")
	switch {
	case len(m.drillable) == 0:
	case m.focus == ooFocusPoints:
		head += dimStyle.Render("          j/k select · enter open · tab back to scroll")
	default:
		head += dimStyle.Render("          tab to select")
	}
	sb.WriteString(head + "\n")

	selectedIdx := -1
	if m.focus == ooFocusPoints && m.selected < len(m.drillable) {
		selectedIdx = m.drillable[m.selected]
	}
	for i, p := range m.points {
		marker, bullet := "  ", "• "
		if len(p.Tickets) > 0 {
			bullet = "• "
		}
		if i == selectedIdx {
			marker = selectedStyle.Render("▸ ")
		}
		for j, line := range p.Lines {
			lead := marker + bullet
			if j > 0 {
				lead = marker + "  "
			}
			if i == selectedIdx {
				line = selectedStyle.Render(line)
			}
			sb.WriteString(lead + line + "\n")
		}
		// Always rendered, so moving the cursor doesn't reflow the list under it.
		if len(p.Tickets) > 0 {
			hint := fmt.Sprintf("      %d ticket%s", len(p.Tickets), plural(len(p.Tickets)))
			if i == selectedIdx {
				hint += " — enter to open"
			}
			sb.WriteString(dimStyle.Render(hint) + "\n")
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderDetail draws the drill-down table for the open talking point.
func (m OneOnOneModel) renderDetail() string {
	if m.detailIdx >= len(m.points) {
		return ""
	}
	p := m.points[m.detailIdx]
	baseURL := strings.TrimRight(m.cfg.Jira.BaseURL, "/")

	var sb strings.Builder
	sb.WriteString(boldStyle.Render(fmt.Sprintf("%s  (%d)", p.Title, len(p.Tickets))) + "\n\n")

	detailW := 0
	if p.DetailHead != "" {
		detailW = len(p.DetailHead)
		for _, r := range p.Tickets {
			if len(r.Detail) > detailW {
				detailW = len(r.Detail)
			}
		}
	}
	sb.WriteString(dimStyle.Render(fmt.Sprintf("    %-12s  %-16s  %*s  %s",
		"KEY", "STATUS", detailW, p.DetailHead, "SUMMARY")) + "\n")

	for i, r := range p.Tickets {
		marker := "  "
		if i == m.detailRow {
			marker = selectedStyle.Render("▸ ")
		}
		// The key is an OSC 8 hyperlink, so cmd-click works in iTerm2, Kitty,
		// WezTerm and Ghostty; enter opens it for terminals that don't.
		sb.WriteString(fmt.Sprintf("%s  %-12s  %-16s  %*s  %s\n",
			marker,
			jiraLink(r.Key, baseURL+"/browse/"+r.Key),
			truncateName(r.Status, 16),
			detailW, r.Detail,
			truncateName(r.Summary, 44)))
	}
	return sb.String()
}

func (m OneOnOneModel) renderCycleTime(carry domain.CarryoverStats) string {
	f := m.flow
	var sb strings.Builder

	if f.IssuesAnalyzed == 0 {
		sb.WriteString(boldStyle.Render("Cycle Time") + "\n")
		sb.WriteString(dimStyle.Render("  No completed-issue history in the last 90 days.") + "\n\n")
		return sb.String()
	}

	sb.WriteString(boldStyle.Render(fmt.Sprintf("Cycle Time  (90 days · %d issues, %d with a usable cycle)",
		f.IssuesAnalyzed, f.IssuesCompleted)) + "\n")

	// p90 over a handful of samples IS just the second-largest value. Showing it
	// would trade one misleading number for another.
	p90 := dimStyle.Render("p90 n/a (too few)")
	if len(f.CycleDays) >= 8 {
		p90 = "p90 " + fmtDays(f.P90CycleDays)
	}
	sb.WriteString(fmt.Sprintf("  %-16s p50 %-8s %s\n", "Start → done", fmtDays(f.P50CycleDays), p90))
	sb.WriteString(dimStyle.Render(
		"                   p50 = typical (half finish faster) · p90 = slow tail (9 in 10 finish faster)") + "\n")

	if total := f.AvgPhase.Total(); total > 0 {
		maxDays := 0.0
		for _, p := range domain.PhaseOrder {
			if f.AvgPhase[p] > maxDays {
				maxDays = f.AvgPhase[p]
			}
		}
		sb.WriteString("\n  " + boldStyle.Render("Where the time goes") +
			dimStyle.Render("   (mean per issue)") + "\n")
		for _, p := range domain.PhaseOrder {
			d := f.AvgPhase[p]
			if d <= 0 {
				continue
			}
			tag := ""
			if p.IsWait() {
				tag = dimStyle.Render("  waiting")
			}
			if p == domain.PhaseUnknown {
				tag = warnStyle.Render("  unclassified")
			}
			sb.WriteString(fmt.Sprintf("    %-16s %-11s %6s  %3.0f%%%s\n",
				p, spBar(d, maxDays, 10), fmtDays(d), f.PhaseShare[p]*100, tag))
		}
		if wait := f.AvgPhase.WaitTotal(); wait > 0 {
			sb.WriteString("    " + dimStyle.Render(strings.Repeat("─", 40)) + "\n")
			line := fmt.Sprintf("    %-16s %-11s %6s  %3.0f%%", "Waiting, total", "", fmtDays(wait), wait/total*100)
			if wait/total > 0.4 {
				line += "  " + warnStyle.Render("⚠ queue-bound")
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}

	if f.P50ReviewDays > 0 {
		sb.WriteString(fmt.Sprintf("  %-16s p50 %s\n", "Review wait", fmtDays(f.P50ReviewDays)))
	}

	rework := fmt.Sprintf("  %-16s %d kicked back from QA (%d times) · %d reopened after done",
		"Rework", f.QABounceIssues, f.QABounceEvents, f.ReopenedIssues)
	if f.QABounceIssues > 0 || f.ReopenedIssues > 0 {
		rework += "  " + warnStyle.Render("⚠")
	}
	sb.WriteString(rework + "\n")

	sb.WriteString(fmt.Sprintf("  %-16s %.1f issues/week over %.0f weeks\n",
		"Throughput", f.ThroughputPerWeek, f.WindowWeeks))

	if carry.SprintsUsed > 0 {
		line := fmt.Sprintf("  %-16s %.0f%% of committed SP rolls over (%d sprint%s)",
			"Carryover", carry.PooledRate*100, carry.SprintsUsed, plural(carry.SprintsUsed))
		if carry.TicketsSized > 0 {
			line += fmt.Sprintf(" · median ticket %.0f SP", carry.MedianTicketSP)
		}
		if carry.PooledRate > 0.3 {
			line += "  " + warnStyle.Render("⚠")
		}
		sb.WriteString(line + "\n")
	}

	if len(f.UnknownStatuses) > 0 {
		sb.WriteString(warnStyle.Render(fmt.Sprintf("  ⚠ Unrecognised statuses: %s",
			strings.Join(f.UnknownStatuses, ", "))) + "\n")
	}
	if f.Truncated > 0 {
		sb.WriteString(dimStyle.Render(fmt.Sprintf(
			"  %d issue%s had more history than Jira returns inline; their numbers may be short.",
			f.Truncated, plural(f.Truncated))) + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func (m OneOnOneModel) renderVelocity(stats []domain.MemberSprintStats,
	recentAvg, histAvg float64, trend string) string {

	var sb strings.Builder
	sb.WriteString(boldStyle.Render("Sprint Velocity") + "  (last 6 sprints)\n")
	if len(stats) == 0 {
		sb.WriteString(dimStyle.Render("  No sprint data available.") + "\n\n")
		return sb.String()
	}
	maxSP := 0.0
	for _, s := range stats {
		if s.CompletedSP > maxSP {
			maxSP = s.CompletedSP
		}
	}
	for i := len(stats) - 1; i >= 0; i-- {
		s := stats[i]
		stateTag := ""
		if strings.ToLower(s.Sprint.State) == "active" {
			stateTag = dimStyle.Render(" [active]")
		}
		sb.WriteString(fmt.Sprintf("  %-26s %-13s%5.0f SP  (%d done)%s\n",
			truncateName(s.Sprint.Name, 26), spBar(s.CompletedSP, maxSP, 12),
			s.CompletedSP, s.CompletedCount, stateTag))
	}
	sb.WriteString("\n")
	// spTrend needs at least two closed sprints to split into halves; below that
	// it returns zeros, and printing "Avg: 0 SP/sprint" reads as a real result.
	if histAvg > 0 || recentAvg > 0 {
		trendLine := fmt.Sprintf("  Avg: %.0f SP/sprint   Recent avg: %.0f", histAvg, recentAvg)
		switch trend {
		case "down":
			sb.WriteString(trendLine + "   " + warnStyle.Render("↓ lower than usual") + "\n")
		case "up":
			sb.WriteString(trendLine + "   " + lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("↑ higher than usual") + "\n")
		default:
			sb.WriteString(trendLine + "   steady\n")
		}
	} else {
		sb.WriteString(dimStyle.Render("  Not enough closed sprints yet for a trend.") + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

func (m OneOnOneModel) renderOpenIssues() string {
	var sb strings.Builder
	baseURL := strings.TrimRight(m.cfg.Jira.BaseURL, "/")
	ageByKey := make(map[string]domain.OpenIssueAge, len(m.openAges))
	for _, a := range m.openAges {
		ageByKey[a.Key] = a
	}

	sb.WriteString(boldStyle.Render(fmt.Sprintf("Open Issues  (%d)", len(m.openIssues))) + "\n")
	if len(m.openIssues) == 0 {
		sb.WriteString(dimStyle.Render("  None — all clear.") + "\n\n")
		return sb.String()
	}
	for _, issue := range m.openIssues {
		age := ""
		if a, ok := ageByKey[issue.Key]; ok && a.DaysInStatus > 0 {
			age = fmtDays(a.DaysInStatus)
			if !a.Exact {
				age = "~" + age
			}
			if a.DaysInStatus >= 14 {
				age = warnStyle.Render(age)
			} else {
				age = dimStyle.Render(age)
			}
		}
		sb.WriteString(fmt.Sprintf("  %-13s  %-12s  %-46s %s\n",
			statusCatLabel(issue.StatusCategory),
			jiraLink(issue.Key, baseURL+"/browse/"+issue.Key),
			truncateName(issue.Summary, 46), age))
	}
	sb.WriteString("\n")
	return sb.String()
}

func (m OneOnOneModel) renderGitActivity(weeks []domain.WeekCount,
	avgCommits float64, thisWeek, activeWeeks, totalCommits int, commitsLow bool) string {

	var sb strings.Builder
	gitFilter := m.member.Email
	if gitFilter == "" {
		gitFilter = m.member.DisplayName
	}
	sb.WriteString(boldStyle.Render("Git Activity") + "  (last 8 weeks)\n")

	switch {
	case len(m.team.Repos) == 0:
		sb.WriteString(dimStyle.Render("  No repositories configured for this team.") + "\n\n")
		return sb.String()
	case totalCommits == 0:
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  No commits found.  (filter: --author=%s)\n", gitFilter)) + "\n")
		return sb.String()
	}

	maxCommits := 0
	for _, w := range weeks {
		if w.Count > maxCommits {
			maxCommits = w.Count
		}
	}
	for _, w := range weeks {
		bar := ""
		if maxCommits > 0 && w.Count > 0 {
			bar = strings.Repeat("█", int(math.Round(float64(w.Count)/float64(maxCommits)*10)))
		}
		sb.WriteString(fmt.Sprintf("  %-11s  %-11s %d commits\n", w.Label, bar, w.Count))
	}
	sb.WriteString("\n")
	line := fmt.Sprintf("  Avg: %.1f/week over %d weeks (%d active)   This week: %d",
		avgCommits, len(weeks), activeWeeks, thisWeek)
	if commitsLow {
		line += "  " + warnStyle.Render("(below average)")
	}
	sb.WriteString(line + "\n\n")
	return sb.String()
}

func (m OneOnOneModel) renderMergeRequests() string {
	return boldStyle.Render("Merge Requests") + "\n" +
		dimStyle.Render("  Not available — GitLab access not configured.") + "\n"
}

// topPhase returns the phase holding the largest share of cycle time.
func topPhase(share domain.PhaseDurations) (domain.Phase, float64) {
	var best domain.Phase
	var bestVal float64
	for _, p := range domain.PhaseOrder {
		if share[p] > bestVal {
			best, bestVal = p, share[p]
		}
	}
	return best, bestVal
}

// staleOpenIssues returns open issues stuck in one status beyond days.
func staleOpenIssues(ages []domain.OpenIssueAge, days float64) []domain.OpenIssueAge {
	var out []domain.OpenIssueAge
	for _, a := range ages {
		if a.DaysInStatus >= days {
			out = append(out, a)
		}
	}
	return out
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
