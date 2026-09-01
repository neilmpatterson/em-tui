package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

type JiraConfig struct {
	BaseURL  string `koanf:"base_url"`
	Email    string `koanf:"email"`
	APIToken string `koanf:"api_token"`
}

type TeamConfig struct {
	Name           string              `koanf:"name"`
	BoardID        int                 `koanf:"board_id"`
	BoardType      string              `koanf:"board_type"`
	Members        []domain.TeamMember `koanf:"members"`
	Repos          []string            `koanf:"repos"`
	Projects       []string            `koanf:"projects"`
	IncomingBugs   string              `koanf:"incoming_bugs"`
	SecurityIssues string              `koanf:"security_issues"`
	WatchStatuses  []string            `koanf:"watch_statuses"`
	Phases         map[string]string   `koanf:"phases"`
}

func (t TeamConfig) EffectiveWatchStatuses() []string {
	if len(t.WatchStatuses) > 0 {
		return t.WatchStatuses
	}
	return []string{"Testing", "Code Review", "Ready for QA"}
}

// defaultPhases maps status name to workflow phase. The names are the ones
// actually in use on this Jira instance. The split that earns its keep is queue
// vs active: "Ready for QA" and "Testing" are both category In Progress, but one
// is a ticket waiting for a person and the other is a person working.
var defaultPhases = map[string]string{
	"In Progress":    "dev",
	"Code Review":    "review",
	"Ready to Merge": "awaiting_merge",
	"Testing":        "qa",
	"Ready for QA":   "awaiting_qa",
	"Bug Fix Needed": "rework",
	"On Hold":        "blocked",
	"Blocked":        "blocked",
}

// EffectivePhases returns the configured status→phase map, or the defaults.
// Statuses absent from the map fall back to keyword matching, so an unrecognised
// status is surfaced as unclassified rather than silently dropped.
func (t TeamConfig) EffectivePhases() map[string]string {
	if len(t.Phases) > 0 {
		return t.Phases
	}
	return defaultPhases
}

type Config struct {
	Jira  JiraConfig   `koanf:"jira"`
	Teams []TeamConfig `koanf:"teams"`
}

// legacyConfig matches the old single-team flat format.
type legacyConfig struct {
	Jira struct {
		BaseURL   string `koanf:"base_url"`
		Email     string `koanf:"email"`
		APIToken  string `koanf:"api_token"`
		BoardID   int    `koanf:"board_id"`
		BoardType string `koanf:"board_type"`
	} `koanf:"jira"`
	Team []domain.TeamMember `koanf:"team"`
	Git  struct {
		Repos []string `koanf:"repos"`
	} `koanf:"git"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "em-tui", "config.yaml")
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}

	k := koanf.New(".")

	fileExists := false
	if _, err := os.Stat(path); err == nil {
		fileExists = true
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}
	}

	_ = k.Load(env.Provider("JIRA_", ".", func(s string) string {
		switch s {
		case "JIRA_API_TOKEN":
			return "jira.api_token"
		default:
			return s
		}
	}), nil)

	// Auto-migrate old flat format (has "team" key, not "teams").
	if fileExists && k.Exists("team") && !k.Exists("teams") {
		return migrateLegacy(k)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}
	// Expand ${VAR} placeholders so config.yaml can use env var references.
	cfg.Jira.BaseURL = os.ExpandEnv(cfg.Jira.BaseURL)
	cfg.Jira.Email = os.ExpandEnv(cfg.Jira.Email)
	cfg.Jira.APIToken = os.ExpandEnv(cfg.Jira.APIToken)
	return &cfg, nil
}

func migrateLegacy(k *koanf.Koanf) (*Config, error) {
	var legacy legacyConfig
	if err := k.Unmarshal("", &legacy); err != nil {
		return nil, fmt.Errorf("migrating legacy config: %w", err)
	}
	cfg := &Config{
		Jira: JiraConfig{
			BaseURL:  legacy.Jira.BaseURL,
			Email:    legacy.Jira.Email,
			APIToken: legacy.Jira.APIToken,
		},
		Teams: []TeamConfig{{
			Name:      "My Team",
			BoardID:   legacy.Jira.BoardID,
			BoardType: legacy.Jira.BoardType,
			Members:   legacy.Team,
			Repos:     legacy.Git.Repos,
		}},
	}
	return cfg, nil
}

func IsSetupRequired(cfg *Config) bool {
	return cfg.Jira.BaseURL == "" || cfg.Jira.Email == "" || cfg.Jira.APIToken == "" || len(cfg.Teams) == 0
}

func Save(cfg *Config, path string) error {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(marshalYAML(cfg)), 0600)
}

func marshalYAML(cfg *Config) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("jira:\n  base_url: %q\n  email: %q\n  api_token: \"${JIRA_API_TOKEN}\"\n\nteams:\n",
		cfg.Jira.BaseURL, cfg.Jira.Email))

	if len(cfg.Teams) == 0 {
		sb.WriteString("  []\n")
		return sb.String()
	}

	for _, t := range cfg.Teams {
		sb.WriteString(fmt.Sprintf("  - name: %q\n    board_id: %d\n    board_type: %q\n",
			t.Name, t.BoardID, t.BoardType))

		if len(t.Members) == 0 {
			sb.WriteString("    members: []\n")
		} else {
			sb.WriteString("    members:\n")
			for _, m := range t.Members {
				sb.WriteString(fmt.Sprintf("      - account_id: %q\n        display_name: %q\n        email: %q\n",
					m.AccountID, m.DisplayName, m.Email))
			}
		}

		if len(t.Repos) == 0 {
			sb.WriteString("    repos: []\n")
		} else {
			sb.WriteString("    repos:\n")
			for _, r := range t.Repos {
				sb.WriteString(fmt.Sprintf("      - %q\n", r))
			}
		}

		if len(t.Projects) > 0 {
			sb.WriteString("    projects:\n")
			for _, p := range t.Projects {
				sb.WriteString(fmt.Sprintf("      - %q\n", p))
			}
		}
		if t.IncomingBugs != "" {
			sb.WriteString(fmt.Sprintf("    incoming_bugs: %q\n", t.IncomingBugs))
		}
		if t.SecurityIssues != "" {
			sb.WriteString(fmt.Sprintf("    security_issues: %q\n", t.SecurityIssues))
		}
		if len(t.WatchStatuses) > 0 {
			sb.WriteString("    watch_statuses:\n")
			for _, s := range t.WatchStatuses {
				sb.WriteString(fmt.Sprintf("      - %q\n", s))
			}
		}
		if len(t.Phases) > 0 {
			// Sorted so a save doesn't reshuffle the file on every write.
			names := make([]string, 0, len(t.Phases))
			for n := range t.Phases {
				names = append(names, n)
			}
			sort.Strings(names)
			sb.WriteString("    phases:\n")
			for _, n := range names {
				sb.WriteString(fmt.Sprintf("      %q: %q\n", n, t.Phases[n]))
			}
		}
	}
	return sb.String()
}
