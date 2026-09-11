package tui

import (
	"testing"
	"time"

	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

// realStatusIDs are status IDs and categories from a real Jira instance
// (anonymised). Note "On Hold" appears twice with different categories, and
// "In Progress" has many IDs across projects — which is exactly why the phase
// lookup keys on ID rather than name.
var realStatusIDs = map[string]string{
	"10522": domain.CatDone,       // Closed
	"11036": domain.CatDone,       // Deployment In Progress
	"10006": domain.CatDone,       // Ready to Deploy
	"11297": domain.CatDone,       // On Hold (the Done-category one)
	"10716": domain.CatTodo,       // Backlog
	"10005": domain.CatInProgress, // Bug Fix Needed
	"10002": domain.CatInProgress, // Code Review
	"3":     domain.CatInProgress, // In Progress
	"10017": domain.CatInProgress, // On Hold (the In-Progress one)
	"10003": domain.CatInProgress, // Ready for QA
	"11302": domain.CatInProgress, // Ready to Merge
	"10004": domain.CatInProgress, // Testing
}

func testModel() OneOnOneModel {
	return OneOnOneModel{
		team:    config.TeamConfig{Name: "CMS"},
		catByID: realStatusIDs,
	}
}

func TestPhaseOf(t *testing.T) {
	m := testModel()
	cases := []struct {
		id, name string
		want     domain.Phase
	}{
		{"10522", "Closed", domain.PhaseDone},
		{"10006", "Ready to Deploy", domain.PhaseDone},
		// Regression: the old isDoneStatus matched strings.Contains(s, "ready to")
		// and classified this as Done, truncating cycle time at merge hand-off.
		{"11302", "Ready to Merge", domain.PhaseAwaitingMerge},
		// Regression: the old isActiveStatus matched strings.Contains(s, "progress")
		// and counted this as active development.
		{"11036", "Deployment In Progress", domain.PhaseDone},
		{"10005", "Bug Fix Needed", domain.PhaseRework},
		{"10003", "Ready for QA", domain.PhaseAwaitingQA},
		{"10004", "Testing", domain.PhaseQA},
		{"10002", "Code Review", domain.PhaseReview},
		{"3", "In Progress", domain.PhaseDev},
		{"10716", "Backlog", domain.PhaseTodo},
		// Same name, different IDs, different categories. Keying by name would
		// have to pick one and be wrong about the other.
		{"10017", "On Hold", domain.PhaseBlocked},
		{"11297", "On Hold", domain.PhaseDone},
	}
	for _, c := range cases {
		if got := m.fc().phaseOf(c.id, c.name); got != c.want {
			t.Errorf("phaseOf(%q, %q) = %v, want %v", c.id, c.name, got, c.want)
		}
	}
}

func TestPhaseOfUnknownStatusIsNotSilentlyBucketed(t *testing.T) {
	m := testModel()
	// Unknown ID, unrecognised name, In Progress category by fallback.
	if got := m.fc().phaseOf("99999", "Spike"); got != domain.PhaseUnknown {
		t.Errorf("phaseOf unknown = %v, want PhaseUnknown", got)
	}
}

func day(n int) time.Time {
	return time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

func tr(n int, fromID, from, toID, to string) domain.StatusTransition {
	return domain.StatusTransition{Timestamp: day(n), FromID: fromID, From: from, ToID: toID, To: to}
}

func TestIssueCyclePhaseAttribution(t *testing.T) {
	m := testModel()
	// Backlog 2d -> Dev 3d -> Review 1d -> AwaitingQA 8d -> QA 1d -> Done
	cl := domain.IssueChangelog{
		Key:     "CMS-1",
		Created: day(0),
		Transitions: []domain.StatusTransition{
			tr(2, "10716", "Backlog", "3", "In Progress"),
			tr(5, "3", "In Progress", "10002", "Code Review"),
			tr(6, "10002", "Code Review", "10003", "Ready for QA"),
			tr(14, "10003", "Ready for QA", "10004", "Testing"),
			tr(15, "10004", "Testing", "10006", "Ready to Deploy"),
		},
	}
	c, unknown := m.fc().issueCycle(cl, false, day(20))
	if len(unknown) != 0 {
		t.Errorf("unexpected unknown statuses: %v", unknown)
	}
	if !c.Completed {
		t.Fatal("expected completed")
	}
	// Cycle runs from first working phase (day 2) to Done (day 15).
	if c.CycleDays != 13 {
		t.Errorf("CycleDays = %v, want 13", c.CycleDays)
	}
	want := map[domain.Phase]float64{
		domain.PhaseTodo:       2, // created -> first transition
		domain.PhaseDev:        3,
		domain.PhaseReview:     1,
		domain.PhaseAwaitingQA: 8,
		domain.PhaseQA:         1,
	}
	for p, w := range want {
		if c.Phases[p] != w {
			t.Errorf("phase %v = %v, want %v", p, c.Phases[p], w)
		}
	}
	// The wait total is what answers "queue problem or coding problem".
	if got := c.Phases.WaitTotal(); got != 8 {
		t.Errorf("WaitTotal = %v, want 8", got)
	}
}

func TestIssueCycleReworkSplit(t *testing.T) {
	m := testModel()
	cl := domain.IssueChangelog{
		Key:     "CMS-2",
		Created: day(0),
		Transitions: []domain.StatusTransition{
			tr(1, "10716", "Backlog", "3", "In Progress"),
			tr(3, "3", "In Progress", "10004", "Testing"),
			// QA bounce: Testing -> Bug Fix Needed
			tr(4, "10004", "Testing", "10005", "Bug Fix Needed"),
			tr(5, "10005", "Bug Fix Needed", "3", "In Progress"),
			tr(6, "3", "In Progress", "10522", "Closed"),
			// Reopen: Closed -> In Progress
			tr(9, "10522", "Closed", "3", "In Progress"),
			tr(10, "3", "In Progress", "10522", "Closed"),
		},
	}
	c, _ := m.fc().issueCycle(cl, false, day(20))
	if c.QABounces != 1 {
		t.Errorf("QABounces = %d, want 1", c.QABounces)
	}
	if c.Reopened != 1 {
		t.Errorf("Reopened = %d, want 1", c.Reopened)
	}
	// The day-6 completion didn't stick, so the clock restarts and the day-10
	// close is the one that counts.
	if c.CycleDays != 9 {
		t.Errorf("CycleDays = %v, want 9 (day 1 -> day 10)", c.CycleDays)
	}
}

// TestIssueCycleStopsAtFirstDone covers a ticket that reached "Ready to Deploy"
// (a Done-category status) and then sat there for six weeks before anyone
// flipped it to Closed. Cycle time must stop at the first Done entry, matching
// `statusCategory IN (Done)`, or the number is dominated by release-train
// latency that has nothing to do with the assignee.
func TestIssueCycleStopsAtFirstDone(t *testing.T) {
	m := testModel()
	cl := domain.IssueChangelog{
		Key:     "ACME-100",
		Created: day(0),
		Transitions: []domain.StatusTransition{
			tr(1, "10716", "Backlog", "3", "In Progress"),
			tr(2, "3", "In Progress", "10002", "Code Review"),
			tr(3, "10002", "Code Review", "10006", "Ready to Deploy"),
			tr(45, "10006", "Ready to Deploy", "11036", "Deployment In Progress"),
			tr(45, "11036", "Deployment In Progress", "10522", "Closed"),
		},
	}
	c, _ := m.fc().issueCycle(cl, false, day(60))
	if c.CycleDays != 2 {
		t.Errorf("CycleDays = %v, want 2 (day 1 -> day 3), not 44", c.CycleDays)
	}
	if c.Reopened != 0 {
		t.Errorf("Reopened = %d, want 0: Done-to-Done moves are not reopens", c.Reopened)
	}
}

func TestCommitsByWeekZeroFills(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) // a Tuesday
	commits := []domain.GitCommit{
		{Date: "2026-09-01"},
		{Date: "2026-09-01"},
		{Date: "2026-08-18"}, // two weeks back
	}
	weeks := commitsByWeek(commits, now, 4)
	if len(weeks) != 4 {
		t.Fatalf("len(weeks) = %d, want 4", len(weeks))
	}
	if weeks[0].Count != 2 {
		t.Errorf("this week = %d, want 2", weeks[0].Count)
	}
	if weeks[1].Count != 0 {
		t.Errorf("last week = %d, want 0", weeks[1].Count)
	}
	if weeks[2].Count != 1 {
		t.Errorf("two weeks back = %d, want 1", weeks[2].Count)
	}
	// The old code divided by active weeks (2), giving 1.5. Over the real 4-week
	// window it is 0.75, which is the honest number.
	avg, thisWeek, active, total := commitCadence(weeks)
	if avg != 0.75 {
		t.Errorf("avg = %v, want 0.75", avg)
	}
	if thisWeek != 2 || active != 2 || total != 3 {
		t.Errorf("thisWeek=%d active=%d total=%d", thisWeek, active, total)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(v, 50); got != 5 {
		t.Errorf("p50 = %v, want 5", got)
	}
	if got := percentile(v, 90); got != 9 {
		t.Errorf("p90 = %v, want 9", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("p50 of empty = %v, want 0", got)
	}
}

func TestCarryoverSkipsUnestimatedSprints(t *testing.T) {
	stats := []domain.MemberSprintStats{
		{Sprint: domain.Sprint{State: "closed"}, CompletedSP: 10, IncompleteSP: 10},
		// All unestimated: 0/0. Counting this as 0% carryover would dilute the
		// rate toward optimism, so it must be skipped.
		{Sprint: domain.Sprint{State: "closed"}, CompletedSP: 0, IncompleteSP: 0},
		// Active sprints don't count: carryover isn't decided until close.
		{Sprint: domain.Sprint{State: "active"}, CompletedSP: 0, IncompleteSP: 40},
	}
	cs := carryoverStats("Someone", stats, nil)
	if cs.SprintsUsed != 1 {
		t.Errorf("SprintsUsed = %d, want 1", cs.SprintsUsed)
	}
	if cs.PooledRate != 0.5 {
		t.Errorf("PooledRate = %v, want 0.5", cs.PooledRate)
	}
}
