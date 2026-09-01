package git

import (
	"os/exec"
	"path/filepath"
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

// FetchCommits returns a Cmd that reads git log for authorEmail in repoPath since sinceDate.
func FetchCommits(accountID, repoPath, authorEmail string, since time.Time) tea.Cmd {
	return func() tea.Msg {
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
