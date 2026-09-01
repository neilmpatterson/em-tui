package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// statusCategoryTTL bounds how stale the cached map may get. Statuses change
// rarely, but a new one added to a workflow would otherwise never be picked up.
const statusCategoryTTL = 7 * 24 * time.Hour

func statusCategoryPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "em-tui", "status-categories.json")
}

// LoadStatusCategories reads the cached status ID → category map. Returns nil
// with no error when the cache is absent or older than statusCategoryTTL, so
// callers fall through to a fetch.
func LoadStatusCategories() (map[string]string, error) {
	path := statusCategoryPath()
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > statusCategoryTTL {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var byID map[string]string
	if err := json.Unmarshal(data, &byID); err != nil {
		return nil, err
	}
	return byID, nil
}

// SaveStatusCategories writes the status ID → category map to the cache.
func SaveStatusCategories(byID map[string]string) error {
	path := statusCategoryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(byID, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
