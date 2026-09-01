package tui

import (
	"strings"
	"testing"

	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

func smokeModel() OneOnOneModel {
	sp := func(v float64) *float64 { return &v }
	m := OneOnOneModel{
		cfg:     &config.Config{Jira: config.JiraConfig{BaseURL: "https://finalsiteinc.atlassian.net"}},
		team:    config.TeamConfig{Name: "CMS"},
		member:  domain.TeamMember{DisplayName: "John Boker"},
		catByID: realStatusIDs,
		width:   100,
		openIssues: []domain.JiraIssue{
			{Key: "CMS-21622", Summary: "Session cf_id guard for V2 endpoints", Status: "In Progress", StatusCategory: "In Progress"},
			{Key: "MSG-1666", Summary: "Legacy feed parser migration", Status: "In Progress", StatusCategory: "In Progress"},
		},
		openAges: []domain.OpenIssueAge{
			{Key: "CMS-21622", DaysInStatus: 3, Exact: true},
			{Key: "MSG-1666", DaysInStatus: 61, Exact: true},
		},
		sprintReports: []domain.SprintReportData{{
			Sprint: domain.Sprint{Name: "Stability Sprint 2026.Q3.3", State: "closed", StartDate: "2026-08-01", EndDate: "2026-08-14"},
			Completed: []domain.SprintIssueCompact{
				{Key: "CMS-20450", Summary: "Playwright coverage for V2 session guard", Assignee: "John Boker", Status: "Closed", FinalSP: sp(3)},
				{Key: "CMS-21394", Summary: "Loop cadence videos through slideshow", Assignee: "John Boker", Status: "Closed", FinalSP: sp(5)},
			},
			NotCompleted: []domain.SprintIssueCompact{
				{Key: "CMS-21501", Summary: "Rework slideshow timing state machine", Assignee: "John Boker", Status: "Bug Fix Needed", FinalSP: sp(8)},
			},
		}},
		flow: domain.MemberFlow{
			IssuesAnalyzed: 12, IssuesCompleted: 10,
			P50CycleDays: 4.1, P90CycleDays: 31.2, MaxCycleDays: 61, MaxCycleKey: "CMS-20450",
			CycleDays:      []float64{1, 2, 3, 4, 4, 5, 8, 12, 30, 61},
			AvgPhase:       domain.PhaseDurations{domain.PhaseDev: 3.2, domain.PhaseReview: 1.8, domain.PhaseAwaitingQA: 19.4, domain.PhaseQA: 1.1},
			PhaseShare:     domain.PhaseDurations{domain.PhaseDev: 0.13, domain.PhaseReview: 0.07, domain.PhaseAwaitingQA: 0.76, domain.PhaseQA: 0.04},
			P50ReviewDays:  1.8,
			QABounceIssues: 2, QABounceEvents: 3, ReopenedIssues: 1, ReopenedEvents: 1,
			ThroughputPerWeek: 2.9, WindowWeeks: 13,
			UnknownStatuses: []string{"Spike"},
			Issues: []domain.IssueCycle{
				{Key: "CMS-20450", CycleDays: 61, Phases: domain.PhaseDurations{domain.PhaseAwaitingQA: 27}, QABounces: 2},
				{Key: "CMS-21394", CycleDays: 30, Phases: domain.PhaseDurations{domain.PhaseAwaitingQA: 12}, Reopened: 1},
				{Key: "CMS-21501", CycleDays: 4, Phases: domain.PhaseDurations{domain.PhaseAwaitingQA: 3}, QABounces: 1},
			},
		},
	}
	stats := memberStatsFromReports(m.member.DisplayName, m.sprintReports)
	carry := carryoverStats(m.member.DisplayName, stats, m.sprintReports)
	m.points = m.buildTalkingPoints(stats, "down", 18, 27, carry, true)
	for i, p := range m.points {
		if len(p.Tickets) > 0 {
			m.drillable = append(m.drillable, i)
		}
	}
	return m
}

func TestTalkingPointsAreDrillable(t *testing.T) {
	m := smokeModel()
	if len(m.drillable) < 4 {
		t.Fatalf("drillable points = %d, want >= 4", len(m.drillable))
	}
	// Every selectable point must actually have tickets behind it, or the cursor
	// lands somewhere enter does nothing.
	for _, i := range m.drillable {
		if len(m.points[i].Tickets) == 0 {
			t.Errorf("point %d is selectable but has no tickets", i)
		}
		if m.points[i].Title == "" {
			t.Errorf("point %d is selectable but has no detail title", i)
		}
	}
	// Points without tickets must not be selectable.
	for i, p := range m.points {
		if len(p.Tickets) > 0 {
			continue
		}
		for _, d := range m.drillable {
			if d == i {
				t.Errorf("point %d has no tickets but is selectable", i)
			}
		}
	}
}

func TestSelectionMarkerTracksSelectedPoint(t *testing.T) {
	m := smokeModel()
	m.focus = ooFocusPoints
	m.selected = 1
	out := m.renderTalkingPoints()
	lines := strings.Split(out, "\n")
	var marked int
	for _, l := range lines {
		if strings.Contains(l, "\u25b8") {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("marked lines = %d, want exactly 1", marked)
	}
	// Scroll focus shows no marker at all.
	m.focus = ooFocusScroll
	if strings.Contains(m.renderTalkingPoints(), "\u25b8") {
		t.Error("marker rendered while focus is on scrolling")
	}
}

func TestDetailTableListsTheRightTickets(t *testing.T) {
	m := smokeModel()
	// The QA-bounce point should list exactly the two issues that bounced.
	var idx = -1
	for i, p := range m.points {
		if strings.HasPrefix(p.Title, "Kicked back") {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no QA-bounce talking point generated")
	}
	m.state = ooStateDetail
	m.detailIdx = idx
	out := m.renderDetail()
	for _, want := range []string{"CMS-20450", "CMS-21501", "BOUNCES"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail view missing %q", want)
		}
	}
	if strings.Contains(out, "CMS-21394") {
		t.Error("detail view lists CMS-21394, which was reopened, not QA-bounced")
	}
	// Summaries come from the sprint reports, not a second fetch.
	if !strings.Contains(out, "Playwright coverage") {
		t.Error("detail view missing the summary looked up from sprint reports")
	}
}

func TestUnknownTicketStillRenders(t *testing.T) {
	m := smokeModel()
	refs := m.refsFor([]string{"ZZZ-999"}, nil)
	if len(refs) != 1 || refs[0].Key != "ZZZ-999" {
		t.Fatalf("refsFor dropped an unknown key: %+v", refs)
	}
}
