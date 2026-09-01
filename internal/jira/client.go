package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gojira "github.com/andygrunwald/go-jira"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

// ErrNoActiveSprint is returned when a board has no active sprint.
var ErrNoActiveSprint = errors.New("no active sprint")

// Section keys used to route SprintIssuesResult messages.
const (
	SectionBugs       = "bugs"
	SectionSecurity   = "security"
	SectionTriage     = "triage"
	SectionInProgress = "inprogress"
	SectionDone       = "done"
	SectionStuck      = "stuck"
	SectionWatch      = "watchstatus"
	SectionMidSprint  = "midsprint"
)

type Client struct {
	jira    *gojira.Client
	baseURL string
	http    *http.Client
}

func New(baseURL, email, token string) (*Client, error) {
	tp := gojira.BasicAuthTransport{
		Username: email,
		Password: token,
	}
	httpClient := &http.Client{Transport: &tp}
	c, err := gojira.NewClient(httpClient, baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		jira:    c,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
	}, nil
}

// --- Result message types ---

type UserSearchResult struct {
	Users []domain.TeamMember
	Err   error
}

// IssueResult carries the accountID so concurrent fetches route correctly.
type IssueResult struct {
	AccountID string
	Issues    []domain.JiraIssue
	Err       error
}

type SprintResult struct {
	Sprint domain.Sprint
	Err    error
}

type FilterResult struct {
	Section string
	JQL     string
	Err     error
}

type SprintIssuesResult struct {
	Section string
	Issues  []domain.JiraIssue
	Err     error
}

// AllSprintIssuesResult carries all issues for a sprint fetched via the board API.
// The board API is inherently board-scoped (respects board filter), unlike plain JQL.
type AllSprintIssuesResult struct {
	Issues []domain.JiraIssue
	Err    error
}

// --- tea.Cmd factories ---

func (c *Client) SearchUsers(query string) tea.Cmd {
	return func() tea.Msg {
		users, _, err := c.jira.User.Find(query)
		if err != nil {
			return UserSearchResult{Err: err}
		}
		var members []domain.TeamMember
		for _, u := range users {
			members = append(members, domain.TeamMember{
				AccountID:   u.AccountID,
				DisplayName: u.DisplayName,
				Email:       u.EmailAddress,
			})
		}
		return UserSearchResult{Users: members}
	}
}

func (c *Client) IssuesAssignedTo(accountID string) tea.Cmd {
	return func() tea.Msg {
		jql := fmt.Sprintf(
			`assignee = "%s" AND statusCategory != Done ORDER BY updated DESC`,
			accountID,
		)
		issues, err := c.searchIssues(jql, 20)
		if err != nil {
			return IssueResult{AccountID: accountID, Err: err}
		}
		return IssueResult{AccountID: accountID, Issues: issues}
	}
}

// FetchActiveSprint fetches the current active sprint for a board via the Agile REST API.
func (c *Client) FetchActiveSprint(boardID int) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/rest/agile/1.0/board/%d/sprint?state=active&maxResults=1", c.baseURL, boardID)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return SprintResult{Err: err}
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return SprintResult{Err: err}
		}
		defer resp.Body.Close()

		var payload struct {
			Values []struct {
				ID        int    `json:"id"`
				Name      string `json:"name"`
				State     string `json:"state"`
				StartDate string `json:"startDate"`
				EndDate   string `json:"endDate"`
			} `json:"values"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return SprintResult{Err: err}
		}
		if len(payload.Values) == 0 {
			return SprintResult{Err: ErrNoActiveSprint}
		}
		v := payload.Values[0]
		start, end := v.StartDate, v.EndDate
		if len(start) >= 10 {
			start = start[:10]
		}
		if len(end) >= 10 {
			end = end[:10]
		}
		return SprintResult{Sprint: domain.Sprint{
			ID:        v.ID,
			Name:      v.Name,
			State:     v.State,
			StartDate: start,
			EndDate:   end,
		}}
	}
}

// FetchFilterJQL resolves a Jira filter ID to its JQL string.
func (c *Client) FetchFilterJQL(section, filterID string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/rest/api/2/filter/%s", c.baseURL, filterID)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return FilterResult{Section: section, Err: err}
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return FilterResult{Section: section, Err: err}
		}
		defer resp.Body.Close()

		var payload struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return FilterResult{Section: section, Err: err}
		}
		return FilterResult{Section: section, JQL: payload.JQL}
	}
}

// FetchSprintIssues executes a JQL search and tags the result with a section key.
func (c *Client) FetchSprintIssues(section, jql string, maxResults int) tea.Cmd {
	return func() tea.Msg {
		issues, err := c.searchIssues(jql, maxResults)
		if err != nil {
			return SprintIssuesResult{Section: section, Err: err}
		}
		return SprintIssuesResult{Section: section, Issues: issues}
	}
}

// FetchBoardSprintIssues fetches ALL issues for a sprint via the Agile board API,
// paginating automatically. The board API respects the board's own filter.
func (c *Client) FetchBoardSprintIssues(boardID, sprintID int) tea.Cmd {
	return func() tea.Msg {
		const pageSize = 100
		fields := "summary,status,priority,assignee,updated,created"
		var all []domain.JiraIssue
		startAt := 0

		for {
			url := fmt.Sprintf(
				"%s/rest/agile/1.0/board/%d/sprint/%d/issue?maxResults=%d&startAt=%d&fields=%s",
				c.baseURL, boardID, sprintID, pageSize, startAt, fields,
			)
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				return AllSprintIssuesResult{Err: err}
			}
			req.Header.Set("Accept", "application/json")

			resp, err := c.http.Do(req)
			if err != nil {
				return AllSprintIssuesResult{Err: err}
			}

			if resp.StatusCode >= 400 {
				resp.Body.Close()
				return AllSprintIssuesResult{Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
			}

			var payload struct {
				Total  int         `json:"total"`
				Issues []issueJSON `json:"issues"`
			}
			err = json.NewDecoder(resp.Body).Decode(&payload)
			resp.Body.Close()
			if err != nil {
				return AllSprintIssuesResult{Err: err}
			}

			for _, i := range payload.Issues {
				all = append(all, i.toDomain())
			}
			startAt += len(payload.Issues)
			if startAt >= payload.Total || len(payload.Issues) == 0 {
				break
			}
		}

		return AllSprintIssuesResult{Issues: all}
	}
}

// FetchAllIssuesByJQL fetches all issues for a JQL query, paginating automatically.
func (c *Client) FetchAllIssuesByJQL(section, jql string) tea.Cmd {
	return func() tea.Msg {
		issues, err := c.searchAllIssues(jql)
		if err != nil {
			return SprintIssuesResult{Section: section, Err: err}
		}
		return SprintIssuesResult{Section: section, Issues: issues}
	}
}

// searchAllIssues paginates through all results for a JQL query using the v3
// search/jql endpoint's cursor-based nextPageToken (startAt is not supported there).
func (c *Client) searchAllIssues(jql string) ([]domain.JiraIssue, error) {
	const pageSize = 100
	var all []domain.JiraIssue
	nextPageToken := ""

	for {
		reqBody := map[string]any{
			"jql":        jql,
			"maxResults": pageSize,
			"fields":     []string{"summary", "status", "priority", "assignee", "updated", "created"},
		}
		if nextPageToken != "" {
			reqBody["nextPageToken"] = nextPageToken
		}

		body, _ := json.Marshal(reqBody)
		req, err := http.NewRequest("POST", c.baseURL+"/rest/api/3/search/jql", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode >= 400 {
			var errBody struct {
				ErrorMessages []string `json:"errorMessages"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&errBody)
			resp.Body.Close()
			if len(errBody.ErrorMessages) > 0 {
				return nil, fmt.Errorf("%s", strings.Join(errBody.ErrorMessages, "; "))
			}
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		var payload struct {
			Issues        []issueJSON `json:"issues"`
			NextPageToken string      `json:"nextPageToken"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		for _, i := range payload.Issues {
			all = append(all, i.toDomain())
		}
		if payload.NextPageToken == "" || len(payload.Issues) == 0 {
			break
		}
		nextPageToken = payload.NextPageToken
	}

	return all, nil
}

// searchIssues calls the Jira REST API v3 search endpoint with a fixed result limit.
func (c *Client) searchIssues(jql string, maxResults int) ([]domain.JiraIssue, error) {
	body, _ := json.Marshal(map[string]any{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "status", "priority", "assignee", "updated", "created"},
	})

	req, err := http.NewRequest("POST", c.baseURL+"/rest/api/3/search/jql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody struct {
			ErrorMessages []string `json:"errorMessages"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if len(errBody.ErrorMessages) > 0 {
			return nil, fmt.Errorf("%s", strings.Join(errBody.ErrorMessages, "; "))
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Issues []issueJSON `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	issues := make([]domain.JiraIssue, 0, len(payload.Issues))
	for _, i := range payload.Issues {
		issues = append(issues, i.toDomain())
	}
	return issues, nil
}

// issueJSON is the shared JSON shape for both REST v3 search and Agile board APIs.
type issueJSON struct {
	Key    string `json:"key"`
	Fields struct {
		Summary  string `json:"summary"`
		Status   struct {
			Name           string `json:"name"`
			StatusCategory struct {
				Name string `json:"name"`
			} `json:"statusCategory"`
		} `json:"status"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
		Updated string `json:"updated"`
		Created string `json:"created"`
	} `json:"fields"`
}

func (i issueJSON) toDomain() domain.JiraIssue {
	assignee := ""
	if i.Fields.Assignee != nil {
		assignee = i.Fields.Assignee.DisplayName
	}
	return domain.JiraIssue{
		Key:            i.Key,
		Summary:        i.Fields.Summary,
		Status:         i.Fields.Status.Name,
		StatusCategory: i.Fields.Status.StatusCategory.Name,
		Priority:       i.Fields.Priority.Name,
		Assignee:       assignee,
		Updated:        truncateDate(i.Fields.Updated),
		Created:        truncateDate(i.Fields.Created),
	}
}

func truncateDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
