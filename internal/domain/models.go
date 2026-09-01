package domain

import "time"

// TeamMember represents a direct report.
type TeamMember struct {
	AccountID   string `koanf:"account_id"`
	DisplayName string `koanf:"display_name"`
	Email       string `koanf:"email"`
	AvatarURL   string `koanf:"avatar_url"`
}

// BoardType is scrum or kanban.
type BoardType string

const (
	BoardTypeScrum  BoardType = "scrum"
	BoardTypeKanban BoardType = "kanban"
)

// Sprint holds basic sprint metadata.
type Sprint struct {
	ID        int
	Name      string
	State     string // "active", "closed", "future"
	StartDate string // "YYYY-MM-DD"
	EndDate   string // "YYYY-MM-DD"
}

// JiraIssue is a lightweight summary of a Jira issue.
type JiraIssue struct {
	Key            string
	Summary        string
	Status         string
	StatusCategory string // "To Do", "In Progress", "Done"
	Priority       string
	Assignee       string
	Updated        string
	Created        string
}

// GitCommit is a single commit from git log.
type GitCommit struct {
	Hash    string
	Author  string
	Date    string
	Message string
	Repo    string // base name of the repository directory
}

// SprintIssueCompact is a lightweight issue record from the GreenHopper sprint report API.
type SprintIssueCompact struct {
	Key       string
	Summary   string
	Assignee  string
	Status    string
	InitialSP *float64 // estimate at sprint start; nil if not estimated
	FinalSP   *float64 // estimate at sprint end
}

// MemberSprintStats is the per-person slice of a single sprint report.
type MemberSprintStats struct {
	Sprint          Sprint
	CompletedSP     float64
	IncompleteSP    float64
	CompletedCount  int
	IncompleteCount int
}

// jiraTimeLayouts covers the two shapes Jira emits for changelog and field
// timestamps: with and without milliseconds.
var jiraTimeLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05-0700",
	time.RFC3339,
}

// ParseJiraTime parses a Jira timestamp, trying each known layout in turn.
func ParseJiraTime(s string) (time.Time, error) {
	var err error
	for _, layout := range jiraTimeLayouts {
		var t time.Time
		if t, err = time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, err
}

// StatusTransition is a single status change event from a Jira issue changelog.
// The IDs are what the category lookup keys on: Jira status *names* are not
// unique across a site (there are two distinct "On Hold" statuses on this
// instance, one Done and one In Progress), but IDs are.
type StatusTransition struct {
	Timestamp time.Time
	From      string
	To        string
	FromID    string
	ToID      string
}

// IssueChangelog holds the ordered status history for one issue.
type IssueChangelog struct {
	Key         string
	Created     time.Time          // anchors the segment before the first transition
	Transitions []StatusTransition // sorted ascending by Timestamp
	Truncated   bool               // history exceeded the inline expand cap
}

// Jira's three status categories, as returned by /rest/api/3/status.
const (
	CatTodo       = "To Do"
	CatInProgress = "In Progress"
	CatDone       = "Done"
)

// Phase is a finer workflow stage than Jira's three status categories. The split
// that matters is queue vs active: "Ready for QA" and "Testing" are both category
// In Progress, but one is a person working and the other is a ticket waiting.
type Phase int

const (
	PhaseUnknown Phase = iota
	PhaseTodo
	PhaseDev
	PhaseReview
	PhaseAwaitingMerge
	PhaseQA
	PhaseAwaitingQA
	PhaseRework
	PhaseBlocked
	PhaseDone
)

// PhaseOrder lists phases in workflow order for rendering.
var PhaseOrder = []Phase{
	PhaseTodo, PhaseDev, PhaseReview, PhaseAwaitingMerge,
	PhaseQA, PhaseAwaitingQA, PhaseRework, PhaseBlocked, PhaseDone, PhaseUnknown,
}

func (p Phase) String() string {
	switch p {
	case PhaseTodo:
		return "To Do"
	case PhaseDev:
		return "Development"
	case PhaseReview:
		return "Review"
	case PhaseAwaitingMerge:
		return "Awaiting merge"
	case PhaseQA:
		return "QA"
	case PhaseAwaitingQA:
		return "Awaiting QA"
	case PhaseRework:
		return "Rework"
	case PhaseBlocked:
		return "Blocked"
	case PhaseDone:
		return "Done"
	}
	return "Unclassified"
}

// IsWait reports whether time in this phase is queue time rather than someone
// actively working. Summing these answers "is this a queue problem?".
func (p Phase) IsWait() bool {
	switch p {
	case PhaseAwaitingMerge, PhaseAwaitingQA, PhaseBlocked:
		return true
	}
	return false
}

// IsWorking reports whether the cycle-time clock should be running. PhaseUnknown
// deliberately does not start it: if a workflow is entirely unclassified we would
// rather report no cycle time and warn than report a confidently wrong number.
func (p Phase) IsWorking() bool {
	switch p {
	case PhaseDev, PhaseReview, PhaseAwaitingMerge, PhaseQA, PhaseAwaitingQA, PhaseRework, PhaseBlocked:
		return true
	}
	return false
}

// PhaseDurations accumulates days per phase, for one issue or summed across many.
type PhaseDurations map[Phase]float64

func (d PhaseDurations) Add(p Phase, days float64) {
	if days > 0 {
		d[p] += days
	}
}

func (d PhaseDurations) Merge(o PhaseDurations) {
	for p, v := range o {
		d[p] += v
	}
}

func (d PhaseDurations) Scale(f float64) PhaseDurations {
	out := make(PhaseDurations, len(d))
	for p, v := range d {
		out[p] = v * f
	}
	return out
}

func (d PhaseDurations) Total() float64 {
	var t float64
	for _, v := range d {
		t += v
	}
	return t
}

// WaitTotal sums only the queue phases.
func (d PhaseDurations) WaitTotal() float64 {
	var t float64
	for p, v := range d {
		if p.IsWait() {
			t += v
		}
	}
	return t
}

// IssueCycle is the per-issue flow breakdown derived from one changelog.
type IssueCycle struct {
	Key       string
	Start     time.Time // first entry into a working phase
	Done      time.Time // last entry into a Done-category status
	Completed bool
	CycleDays float64 // Start → Done
	Phases    PhaseDurations
	Reopened  int
	QABounces int
}

// WeekCount is a single ISO-week bucket, label formatted "2026-W12".
type WeekCount struct {
	Label string
	Count int
}

// OpenIssueAge is the WIP-age view of one currently-open issue.
type OpenIssueAge struct {
	Key          string
	DaysInStatus float64
	Exact        bool // false when derived from the Updated field rather than the changelog
}

// CarryoverStats summarises how much committed work rolls over, plus typical size.
type CarryoverStats struct {
	PooledRate     float64 // Σ incomplete / Σ total across closed sprints
	SprintsUsed    int
	MedianTicketSP float64
	TicketsSized   int
}

// MemberFlow holds computed flow metrics derived from issue changelogs.
type MemberFlow struct {
	IssuesAnalyzed  int // changelogs for completed issues that were fetched
	IssuesCompleted int // of those, ones with a usable Start → Done cycle

	// Per-issue cycle times, sorted ascending. Retained rather than collapsed to a
	// mean so we can report percentiles: one zombie ticket drags a mean badly.
	CycleDays    []float64
	P50CycleDays float64
	P90CycleDays float64
	MaxCycleDays float64
	MaxCycleKey  string

	AvgPhase   PhaseDurations // mean days per phase across completed issues
	PhaseShare PhaseDurations // each phase's share of total time, 0..1

	ReviewDays    []float64 // one sample per stay in review
	P50ReviewDays float64

	ReopenedIssues int
	ReopenedEvents int
	QABounceIssues int
	QABounceEvents int

	DoneByWeek        []WeekCount
	ThroughputPerWeek float64
	WindowWeeks       float64

	// Status names no classifier recognised, deduped and sorted. Surfaced in the
	// UI so the phase table gets tuned instead of silently mis-bucketing.
	UnknownStatuses []string
	Truncated       int // issues whose changelog exceeded the inline cap
}

// SprintReportData holds sprint report data from the GreenHopper API.
type SprintReportData struct {
	Sprint         Sprint
	Completed      []SprintIssueCompact
	NotCompleted   []SprintIssueCompact
	Punted         []SprintIssueCompact
	AddedMidSprint map[string]bool // issue keys added after sprint start
	// SP totals; nil means the board has no story points field configured
	PlannedSP    *float64 // committed SP at sprint start (before mid-sprint changes)
	CompletedSP  *float64 // SP completed by sprint end
	IncompleteSP *float64 // SP not completed by sprint end
	TotalEndSP   *float64 // all issues combined at end of sprint
	AddedSP      *float64 // SP added to sprint after start (mid-sprint scope additions)
	RemovedSP    *float64 // SP removed from sprint (punted issues)
}
