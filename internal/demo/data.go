package demo

import (
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

// DemoConfig returns a self-contained config for the Acme Corp demo.
func DemoConfig() *config.Config {
	return &config.Config{
		Jira: config.JiraConfig{
			BaseURL:  "https://acme.atlassian.net",
			Email:    "alex.chen@acme.com",
			APIToken: "demo",
		},
		Teams: []config.TeamConfig{DemoTeam()},
	}
}

// DemoTeam returns the Platform team config.
func DemoTeam() config.TeamConfig {
	return config.TeamConfig{
		Name:      "Platform",
		BoardID:   42,
		BoardType: "scrum",
		Members: []domain.TeamMember{
			{AccountID: "alex", DisplayName: "Alex Chen", Email: "alex.chen@acme.com"},
			{AccountID: "sam", DisplayName: "Sam Rivera", Email: "sam.rivera@acme.com"},
			{AccountID: "jordan", DisplayName: "Jordan Lee", Email: "jordan.lee@acme.com"},
			{AccountID: "casey", DisplayName: "Casey Kim", Email: "casey.kim@acme.com"},
		},
	}
}

// StatusCategories returns the status ID → category name map used by catByID.
func StatusCategories() map[string]string {
	return map[string]string{
		"10001": "To Do",
		"10002": "In Progress",
		"10003": "In Progress",
		"10004": "In Progress",
		"10005": "Done",
		"10006": "In Progress",
	}
}
