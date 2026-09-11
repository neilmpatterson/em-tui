package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

// CommitResult is the message returned by FetchCommits.
// AccountID tags which team member this result belongs to so concurrent
// fetches across members and repos route correctly in Update.
type CommitResult struct {
	AccountID string
	Commits   []domain.GitCommit
	Err       error
}

// expandTilde resolves a leading ~ to the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// CommitDirResult is the message returned by FetchCommitDirs.
type CommitDirResult struct {
	AccountID string
	Dirs      []domain.DirStat
	Err       error
}

// dirKey returns the grouping key for a file path: the first two segments joined
// with "/" when depth >= 3, or just the first segment otherwise.
func dirKey(path string) string {
	segs := strings.SplitN(path, "/", 3)
	if len(segs) >= 3 {
		return segs[0] + "/" + segs[1]
	}
	return segs[0]
}

// FetchCommitDirs returns a Cmd that groups commits by top-level directory for
// authorEmail in repoPath since sinceDate. Uses --numstat to get per-file paths.
func FetchCommitDirs(accountID, repoPath, authorEmail string, since time.Time) tea.Cmd {
	return func() tea.Msg {
		repoPath = expandTilde(repoPath)
		args := []string{
			"-C", repoPath,
			"log",
			"--all",
			"--author=" + authorEmail,
			"--since=" + since.Format("2006-01-02"),
			"--pretty=format:COMMIT",
			"--numstat",
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			return CommitDirResult{AccountID: accountID, Err: err}
		}
		counts := make(map[string]int)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" || line == "COMMIT" {
				continue
			}
			// numstat lines: "<added>\t<deleted>\t<path>"
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			path := parts[2]
			if path == "" {
				continue
			}
			counts[dirKey(path)]++
		}
		dirs := make([]domain.DirStat, 0, len(counts))
		for d, c := range counts {
			dirs = append(dirs, domain.DirStat{Dir: d, Commits: c})
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].Commits > dirs[j].Commits })
		return CommitDirResult{AccountID: accountID, Dirs: dirs}
	}
}

// FetchCommits returns a Cmd that reads git log for authorEmail in repoPath since sinceDate.
func FetchCommits(accountID, repoPath, authorEmail string, since time.Time) tea.Cmd {
	return func() tea.Msg {
		repoPath = expandTilde(repoPath)
		repoName := filepath.Base(repoPath)
		args := []string{
			"-C", repoPath,
			"log",
			"--all",
			"--author=" + authorEmail,
			"--since=" + since.Format("2006-01-02"),
			"--pretty=format:%H\x1f%an\x1f%ad\x1f%s",
			"--date=short",
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			return CommitResult{AccountID: accountID, Err: err}
		}
		var commits []domain.GitCommit
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\x1f", 4)
			if len(parts) < 4 {
				continue
			}
			hash := parts[0]
			if len(hash) > 8 {
				hash = hash[:8]
			}
			commits = append(commits, domain.GitCommit{
				Hash:    hash,
				Author:  parts[1],
				Date:    parts[2],
				Message: parts[3],
				Repo:    repoName,
			})
		}
		return CommitResult{AccountID: accountID, Commits: commits}
	}
}
