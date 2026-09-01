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

// StatusTransition is a single status change event from a Jira issue changelog.
type StatusTransition struct {
	Timestamp time.Time
	From      string
	To        string
}

// IssueChangelog holds the ordered status history for one issue.
type IssueChangelog struct {
	Key         string
	Transitions []StatusTransition // sorted ascending by Timestamp
}

// MemberDORA holds computed DORA-adjacent metrics derived from issue changelogs.
type MemberDORA struct {
	AvgCycleTimeDays  float64 // first active status → Done, averaged across issues
	AvgCodeReviewDays float64 // time spent in Code Review per pass, averaged
	ReworkCount       int     // issues with ≥1 regressive status transition
	IssuesAnalyzed    int
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
