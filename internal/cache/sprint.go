package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/neilmpatterson/em-tui/internal/domain"
)

func sprintDir(boardID int) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "em-tui", "sprints", fmt.Sprintf("%d", boardID))
}

// LoadSprint reads a cached sprint report. Returns nil, nil if the cache entry
// doesn't exist yet.
func LoadSprint(boardID, sprintID int) (*domain.SprintReportData, error) {
	path := filepath.Join(sprintDir(boardID), fmt.Sprintf("%d.json", sprintID))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var report domain.SprintReportData
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

// SaveSprint writes a sprint report to the cache. Only call for closed sprints.
func SaveSprint(boardID, sprintID int, report domain.SprintReportData) error {
	dir := sprintDir(boardID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.json", sprintID))
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
