package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/neilmpatterson/em-tui/internal/domain"
)

const statsHistoryTTL = 4 * time.Hour

type statsHistoryFile struct {
	SavedAt time.Time          `json:"saved_at"`
	Issues  []domain.JiraIssue `json:"issues"`
}

func statsHistoryPath(accountID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "em-tui", "stats", accountID+"-history.json")
}

// LoadHistoricalIssues reads a cached 18-month issue list. Returns nil, nil on
// cache miss or when the entry is older than statsHistoryTTL.
func LoadHistoricalIssues(accountID string) ([]domain.JiraIssue, error) {
	data, err := os.ReadFile(statsHistoryPath(accountID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f statsHistoryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if time.Since(f.SavedAt) > statsHistoryTTL {
		return nil, nil
	}
	return f.Issues, nil
}

// SaveHistoricalIssues writes the issue list to the stats cache.
func SaveHistoricalIssues(accountID string, issues []domain.JiraIssue) error {
	path := statsHistoryPath(accountID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f := statsHistoryFile{SavedAt: time.Now(), Issues: issues}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
