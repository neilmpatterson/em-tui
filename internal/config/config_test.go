package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neilmpatterson/em-tui/internal/domain"
)

func TestIsSetupRequired_ZeroValue(t *testing.T) {
	cfg := &Config{}
	if !IsSetupRequired(cfg) {
		t.Error("zero-value Config should require setup")
	}
}

func TestIsSetupRequired_Populated(t *testing.T) {
	cfg := &Config{
		Jira: JiraConfig{
			BaseURL:  "https://acme.atlassian.net",
			Email:    "alex@acme.com",
			APIToken: "token123",
		},
		Teams: []TeamConfig{{
			Name: "Platform",
			Members: []domain.TeamMember{
				{AccountID: "alex", DisplayName: "Alex Chen"},
			},
		}},
	}
	if IsSetupRequired(cfg) {
		t.Error("fully populated Config should not require setup")
	}
}

func TestIsSetupRequired_MissingToken(t *testing.T) {
	cfg := &Config{
		Jira: JiraConfig{BaseURL: "https://acme.atlassian.net", Email: "alex@acme.com"},
		Teams: []TeamConfig{{Name: "Platform"}},
	}
	if !IsSetupRequired(cfg) {
		t.Error("missing APIToken should require setup")
	}
}

func TestIsSetupRequired_NoTeams(t *testing.T) {
	cfg := &Config{
		Jira: JiraConfig{
			BaseURL:  "https://acme.atlassian.net",
			Email:    "alex@acme.com",
			APIToken: "tok",
		},
	}
	if !IsSetupRequired(cfg) {
		t.Error("no teams should require setup")
	}
}

func TestEffectivePhases_Defaults(t *testing.T) {
	tc := TeamConfig{} // empty Phases
	phases := tc.EffectivePhases()
	if len(phases) == 0 {
		t.Error("empty Phases should return non-empty defaults")
	}
	if phases["In Progress"] == "" {
		t.Error("defaults should include In Progress mapping")
	}
}

func TestEffectivePhases_Custom(t *testing.T) {
	tc := TeamConfig{
		Phases: map[string]string{"My Status": "dev"},
	}
	phases := tc.EffectivePhases()
	if phases["My Status"] != "dev" {
		t.Errorf("custom phases not returned: got %v", phases)
	}
	// Custom map should be returned as-is, not merged with defaults.
	if _, ok := phases["In Progress"]; ok {
		t.Error("custom phases should not include defaults")
	}
}

func TestEffectiveWatchStatuses_Defaults(t *testing.T) {
	tc := TeamConfig{}
	ws := tc.EffectiveWatchStatuses()
	if len(ws) == 0 {
		t.Error("empty WatchStatuses should return non-empty defaults")
	}
}

func TestEffectiveWatchStatuses_Custom(t *testing.T) {
	tc := TeamConfig{WatchStatuses: []string{"My Status"}}
	ws := tc.EffectiveWatchStatuses()
	if len(ws) != 1 || ws[0] != "My Status" {
		t.Errorf("expected [My Status], got %v", ws)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	// Write a config that holds a placeholder — the env provider will override it.
	yaml := `jira:
  base_url: "https://acme.atlassian.net"
  email: "alex@acme.com"
  api_token: "from-file"

teams: []
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	// The env provider maps JIRA_API_TOKEN → jira.api_token and takes precedence
	// over the file value.
	t.Setenv("JIRA_API_TOKEN", "env-token-test-value")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Jira.APIToken != "env-token-test-value" {
		t.Errorf("APIToken = %q, want env-token-test-value", cfg.Jira.APIToken)
	}
}
