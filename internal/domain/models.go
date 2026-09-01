package domain

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
