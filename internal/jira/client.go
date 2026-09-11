package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	gojira "github.com/andygrunwald/go-jira"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/neilmpatterson/em-tui/internal/cache"
	"github.com/neilmpatterson/em-tui/internal/domain"
)

// ErrNoActiveSprint is returned when a board has no active sprint.
var ErrNoActiveSprint = errors.New("no active sprint")

// Section keys used to route SprintIssuesResult messages.
const (
	SectionBugs        = "bugs"
	SectionSecurity    = "security"
	SectionTriage      = "triage"
	SectionInProgress  = "inprogress"
	SectionDone        = "done"
	SectionStuck       = "stuck"
	SectionWatch       = "watchstatus"
	SectionMidSprint   = "midsprint"
	SectionStatsHistory = "stats_history"
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

// ChangelogsResult carries status-transition histories for a batch of issues.
type ChangelogsResult struct {
	AccountID  string
	Changelogs []domain.IssueChangelog
	Err        error
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

// FetchHistoricalIssues fetches all Done issues assigned to accountID in the last
// months months. Used by the Stats screen for the 18-month ticket breakdown.
func (c *Client) FetchHistoricalIssues(accountID string, months int) tea.Cmd {
	since := time.Now().AddDate(0, -months, 0).Format("2006-01-02")
	jql := fmt.Sprintf(
		`assignee = "%s" AND statusCategory = Done AND updated >= "%s" ORDER BY updated DESC`,
		accountID, since,
	)
	return c.FetchAllIssuesByJQL(SectionStatsHistory, jql)
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
			"fields":     []string{"summary", "status", "priority", "assignee", "updated", "created", "issuetype"},
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
		"fields":     []string{"summary", "status", "priority", "assignee", "updated", "created", "issuetype"},
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
		Summary string `json:"summary"`
		Status  struct {
			Name           string `json:"name"`
			StatusCategory struct {
				Name string `json:"name"`
			} `json:"statusCategory"`
		} `json:"status"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
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
		IssueType:      i.Fields.IssueType.Name,
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

// --- Sprint list and report ---

// SprintsResult carries a merged list of sprints (active first, then recent closed).
type SprintsResult struct {
	Sprints []domain.Sprint
	Err     error
}

// SprintReportResult carries the full sprint report from the GreenHopper API.
type SprintReportResult struct {
	Data domain.SprintReportData
	Err  error
}

// FetchRecentSprints fetches the active sprint (if any) plus the most recently closed
// sprints for a board, returning them merged with active first.
func (c *Client) FetchRecentSprints(boardID, limit int) tea.Cmd {
	return func() tea.Msg {
		var sprints []domain.Sprint

		activeURL := fmt.Sprintf("%s/rest/agile/1.0/board/%d/sprint?state=active&maxResults=1", c.baseURL, boardID)
		if active, err := c.fetchSprintPage(activeURL); err == nil {
			sprints = append(sprints, active...)
		}

		// Paginate all closed sprints so we get the most recent, not just the first page.
		closed := c.fetchAllClosedSprints(boardID)
		sort.Slice(closed, func(i, j int) bool {
			return closed[i].EndDate > closed[j].EndDate
		})
		if len(closed) > limit {
			closed = closed[:limit]
		}
		sprints = append(sprints, closed...)

		if len(sprints) == 0 {
			return SprintsResult{Err: fmt.Errorf("no sprints found for board %d", boardID)}
		}
		return SprintsResult{Sprints: sprints}
	}
}

// fetchAllClosedSprints paginates the Agile API to collect every closed sprint for a board.
func (c *Client) fetchAllClosedSprints(boardID int) []domain.Sprint {
	const pageSize = 50
	var all []domain.Sprint
	startAt := 0

	for {
		url := fmt.Sprintf(
			"%s/rest/agile/1.0/board/%d/sprint?state=closed&maxResults=%d&startAt=%d",
			c.baseURL, boardID, pageSize, startAt,
		)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			break
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			break
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			break
		}

		var payload struct {
			Values []struct {
				ID        int    `json:"id"`
				Name      string `json:"name"`
				State     string `json:"state"`
				StartDate string `json:"startDate"`
				EndDate   string `json:"endDate"`
			} `json:"values"`
			IsLast bool `json:"isLast"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			break
		}

		for _, v := range payload.Values {
			start, end := v.StartDate, v.EndDate
			if len(start) >= 10 {
				start = start[:10]
			}
			if len(end) >= 10 {
				end = end[:10]
			}
			all = append(all, domain.Sprint{
				ID:        v.ID,
				Name:      v.Name,
				State:     v.State,
				StartDate: start,
				EndDate:   end,
			})
		}

		if payload.IsLast || len(payload.Values) == 0 {
			break
		}
		startAt += len(payload.Values)
	}
	return all
}

// fetchSprintPage fetches a page of sprints from the given Agile API URL.
func (c *Client) fetchSprintPage(url string) ([]domain.Sprint, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

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
		return nil, err
	}

	sprints := make([]domain.Sprint, 0, len(payload.Values))
	for _, v := range payload.Values {
		start, end := v.StartDate, v.EndDate
		if len(start) >= 10 {
			start = start[:10]
		}
		if len(end) >= 10 {
			end = end[:10]
		}
		sprints = append(sprints, domain.Sprint{
			ID:        v.ID,
			Name:      v.Name,
			State:     v.State,
			StartDate: start,
			EndDate:   end,
		})
	}
	return sprints, nil
}

// ghIssueJSON is the Jira GreenHopper sprint report issue shape.
type ghIssueJSON struct {
	Key               string `json:"key"`
	Summary           string `json:"summary"`
	AssigneeName      string `json:"assigneeName"`
	StatusName        string `json:"statusName"`
	PriorityName      string `json:"priorityName"`
	EstimateStatistic struct {
		StatFieldValue *struct {
			Value *float64 `json:"value"`
		} `json:"statFieldValue"`
	} `json:"estimateStatistic"`
	CurrentEstimateStatistic struct {
		StatFieldValue *struct {
			Value *float64 `json:"value"`
		} `json:"statFieldValue"`
	} `json:"currentEstimateStatistic"`
}

// toCompact converts a GreenHopper issue to SprintIssueCompact. spMap provides a
// fallback story-point value (from the Agile board API) for when GreenHopper's
// estimate statistic is null.
func (g ghIssueJSON) toCompact(spMap map[string]float64) domain.SprintIssueCompact {
	var initialSP, finalSP *float64
	if g.EstimateStatistic.StatFieldValue != nil {
		initialSP = g.EstimateStatistic.StatFieldValue.Value
	}
	if g.CurrentEstimateStatistic.StatFieldValue != nil {
		finalSP = g.CurrentEstimateStatistic.StatFieldValue.Value
	}
	if finalSP == nil {
		if sp, ok := spMap[g.Key]; ok {
			finalSP = &sp
		}
	}
	return domain.SprintIssueCompact{
		Key:       g.Key,
		Summary:   g.Summary,
		Assignee:  g.AssigneeName,
		Status:    g.StatusName,
		Priority:  g.PriorityName,
		InitialSP: initialSP,
		FinalSP:   finalSP,
	}
}

// fetchSprintIssueSP fetches the story_points field for every issue in a sprint via
// the Agile board API. Used as a fallback when GreenHopper estimate statistics are null.
func (c *Client) fetchSprintIssueSP(boardID, sprintID int) map[string]float64 {
	result := make(map[string]float64)
	const pageSize = 100
	startAt := 0

	for {
		url := fmt.Sprintf(
			"%s/rest/agile/1.0/board/%d/sprint/%d/issue?maxResults=%d&startAt=%d&fields=story_points",
			c.baseURL, boardID, sprintID, pageSize, startAt,
		)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			break
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			break
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			break
		}

		var payload struct {
			Total  int `json:"total"`
			Issues []struct {
				Key    string `json:"key"`
				Fields struct {
					StoryPoints *float64 `json:"story_points"`
				} `json:"fields"`
			} `json:"issues"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil || len(payload.Issues) == 0 {
			break
		}

		for _, i := range payload.Issues {
			if i.Fields.StoryPoints != nil {
				result[i.Key] = *i.Fields.StoryPoints
			}
		}

		startAt += len(payload.Issues)
		if startAt >= payload.Total {
			break
		}
	}
	return result
}

// ghEstimateSum holds a GreenHopper story-point sum which may be null.
type ghEstimateSum struct {
	Value *float64 `json:"value"`
}

// FetchSprintReport fetches the sprint report from the GreenHopper API.
// The provided sprint is embedded in the result so we avoid re-parsing GreenHopper's
// non-standard date format.
func (c *Client) FetchSprintReport(boardID int, sprint domain.Sprint) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf(
			"%s/rest/greenhopper/1.0/rapid/charts/sprintreport?rapidViewId=%d&sprintId=%d",
			c.baseURL, boardID, sprint.ID,
		)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return SprintReportResult{Err: err}
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return SprintReportResult{Err: err}
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			return SprintReportResult{Err: fmt.Errorf("HTTP %d fetching sprint report", resp.StatusCode)}
		}

		var payload struct {
			Contents struct {
				CompletedIssues       []ghIssueJSON   `json:"completedIssues"`
				NotCompleted          []ghIssueJSON   `json:"issuesNotCompletedInCurrentSprint"`
				PuntedIssues          []ghIssueJSON   `json:"puntedIssues"`
				AddedDuringSprint     map[string]bool `json:"issueKeysAddedDuringSprint"`
				CompletedInitialSum   ghEstimateSum   `json:"completedIssuesInitialEstimateSum"`
				CompletedFinalSum     ghEstimateSum   `json:"completedIssueEstimateSum"`
				IncompletedInitialSum ghEstimateSum   `json:"incompletedIssuesInitialEstimateSum"`
				IncompletedFinalSum   ghEstimateSum   `json:"incompletedIssuesEstimateSum"`
				AllIssuesSum          ghEstimateSum   `json:"allIssuesEstimateSum"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return SprintReportResult{Err: err}
		}

		cnts := payload.Contents

		// Fetch per-issue SP from the Agile board API as a fallback for when
		// GreenHopper's estimate statistics are null (varies by Jira configuration).
		spMap := c.fetchSprintIssueSP(boardID, sprint.ID)

		completed := make([]domain.SprintIssueCompact, 0, len(cnts.CompletedIssues))
		for _, i := range cnts.CompletedIssues {
			completed = append(completed, i.toCompact(spMap))
		}
		notCompleted := make([]domain.SprintIssueCompact, 0, len(cnts.NotCompleted))
		for _, i := range cnts.NotCompleted {
			notCompleted = append(notCompleted, i.toCompact(spMap))
		}
		punted := make([]domain.SprintIssueCompact, 0, len(cnts.PuntedIssues))
		for _, i := range cnts.PuntedIssues {
			punted = append(punted, i.toCompact(spMap))
		}

		// Use GreenHopper aggregate sums when available; otherwise compute from per-issue SP.
		completedSP := cnts.CompletedFinalSum.Value
		if completedSP == nil {
			completedSP = spSumOf(completed)
		}
		incompleteSP := cnts.IncompletedFinalSum.Value
		if incompleteSP == nil {
			incompleteSP = spSumOf(notCompleted)
		}

		// PlannedSP: initial estimate sum from GreenHopper, or approximate as current SP
		// of issues that were in the sprint at start (excludes mid-sprint additions).
		plannedSP := sumSP(cnts.CompletedInitialSum.Value, cnts.IncompletedInitialSum.Value)
		if plannedSP == nil {
			var total float64
			hasAny := false
			for _, iss := range append(completed, notCompleted...) {
				if !cnts.AddedDuringSprint[iss.Key] && iss.FinalSP != nil {
					total += *iss.FinalSP
					hasAny = true
				}
			}
			if hasAny {
				plannedSP = &total
			}
		}

		// SP added mid-sprint and removed (punted).
		var addedSP *float64
		if len(cnts.AddedDuringSprint) > 0 {
			var total float64
			hasAny := false
			for _, iss := range append(completed, notCompleted...) {
				if cnts.AddedDuringSprint[iss.Key] && iss.FinalSP != nil {
					total += *iss.FinalSP
					hasAny = true
				}
			}
			if hasAny {
				addedSP = &total
			}
		}
		removedSP := spSumOf(punted)

		data := domain.SprintReportData{
			Sprint:         sprint,
			Completed:      completed,
			NotCompleted:   notCompleted,
			Punted:         punted,
			AddedMidSprint: cnts.AddedDuringSprint,
			PlannedSP:      plannedSP,
			CompletedSP:    completedSP,
			IncompleteSP:   incompleteSP,
			TotalEndSP:     cnts.AllIssuesSum.Value,
			AddedSP:        addedSP,
			RemovedSP:      removedSP,
		}
		return SprintReportResult{Data: data}
	}
}

// spSumOf sums FinalSP across a slice of issues; returns nil if none have SP set.
func spSumOf(issues []domain.SprintIssueCompact) *float64 {
	var total float64
	hasAny := false
	for _, iss := range issues {
		if iss.FinalSP != nil {
			total += *iss.FinalSP
			hasAny = true
		}
	}
	if !hasAny {
		return nil
	}
	return &total
}

// sumSP adds two nullable SP values; returns nil only if both inputs are nil.
func sumSP(a, b *float64) *float64 {
	if a == nil && b == nil {
		return nil
	}
	var va, vb float64
	if a != nil {
		va = *a
	}
	if b != nil {
		vb = *b
	}
	v := va + vb
	return &v
}

// FetchChangelogs fetches status-transition histories for a batch of issue keys.
// Up to 10 requests run concurrently; per-issue errors are silently skipped.
func (c *Client) FetchChangelogs(accountID string, issueKeys []string) tea.Cmd {
	return func() tea.Msg {
		if len(issueKeys) == 0 {
			return ChangelogsResult{AccountID: accountID}
		}
		type result struct {
			cl  domain.IssueChangelog
			err error
		}
		sem := make(chan struct{}, 10)
		ch := make(chan result, len(issueKeys))
		for _, key := range issueKeys {
			k := key
			go func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				cl, err := c.fetchIssueChangelog(k)
				ch <- result{cl, err}
			}()
		}
		changelogs := make([]domain.IssueChangelog, 0, len(issueKeys))
		for range issueKeys {
			r := <-ch
			if r.err == nil {
				changelogs = append(changelogs, r.cl)
			}
		}
		return ChangelogsResult{AccountID: accountID, Changelogs: changelogs}
	}
}

type changelogResp struct {
	Fields struct {
		Created string `json:"created"`
	} `json:"fields"`
	Changelog struct {
		Total     int `json:"total"`
		Histories []struct {
			Created string `json:"created"`
			Items   []struct {
				Field      string `json:"field"`
				From       string `json:"from"`
				To         string `json:"to"`
				FromString string `json:"fromString"`
				ToString   string `json:"toString"`
			} `json:"items"`
		} `json:"histories"`
	} `json:"changelog"`
}

// fetchIssueChangelog fetches one issue's creation time and status-transition
// history in a single request. Created anchors the segment before the first
// transition, which is the issue's backlog time.
func (c *Client) fetchIssueChangelog(key string) (domain.IssueChangelog, error) {
	url := fmt.Sprintf("%s/rest/api/3/issue/%s?expand=changelog&fields=created", c.baseURL, key)
	resp, err := c.http.Get(url)
	if err != nil {
		return domain.IssueChangelog{}, err
	}
	defer resp.Body.Close()
	// Without this a 403 or 404 decodes cleanly into an empty changelog and the
	// issue lands in the result set contributing nothing but inflating the count.
	if resp.StatusCode >= 400 {
		return domain.IssueChangelog{}, fmt.Errorf("jira: HTTP %d fetching changelog for %s", resp.StatusCode, key)
	}
	var data changelogResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return domain.IssueChangelog{}, err
	}

	cl := domain.IssueChangelog{Key: key}
	cl.Created, _ = domain.ParseJiraTime(data.Fields.Created)
	cl.Truncated = data.Changelog.Total > len(data.Changelog.Histories)

	for _, h := range data.Changelog.Histories {
		for _, item := range h.Items {
			if item.Field != "status" {
				continue
			}
			t, err := domain.ParseJiraTime(h.Created)
			if err != nil {
				continue
			}
			cl.Transitions = append(cl.Transitions, domain.StatusTransition{
				Timestamp: t,
				From:      item.FromString,
				To:        item.ToString,
				FromID:    item.From,
				ToID:      item.To,
			})
		}
	}
	// Stable: several status items can share one history's timestamp, and an
	// unstable sort would reorder them, breaking From/To adjacency and inventing
	// rework transitions that never happened.
	sort.SliceStable(cl.Transitions, func(i, j int) bool {
		return cl.Transitions[i].Timestamp.Before(cl.Transitions[j].Timestamp)
	})
	return cl, nil
}

// StatusCategoriesResult carries the status ID → category name map.
type StatusCategoriesResult struct {
	ByID map[string]string
	Err  error
}

// FetchStatusCategories loads every status on the site with its category. We key
// by ID rather than name because names are not unique across a Jira site: this
// instance has two distinct "On Hold" statuses in different categories, so a
// name-keyed map would mis-bucket one of them.
func (c *Client) FetchStatusCategories() tea.Cmd {
	return func() tea.Msg {
		if cached, err := cache.LoadStatusCategories(); err == nil && len(cached) > 0 {
			return StatusCategoriesResult{ByID: cached}
		}
		url := fmt.Sprintf("%s/rest/api/3/status", c.baseURL)
		resp, err := c.http.Get(url)
		if err != nil {
			return StatusCategoriesResult{Err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return StatusCategoriesResult{Err: fmt.Errorf("jira: HTTP %d fetching statuses", resp.StatusCode)}
		}
		var data []struct {
			ID             string `json:"id"`
			StatusCategory struct {
				Name string `json:"name"`
			} `json:"statusCategory"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return StatusCategoriesResult{Err: err}
		}
		byID := make(map[string]string, len(data))
		for _, s := range data {
			byID[s.ID] = s.StatusCategory.Name
		}
		go func() { _ = cache.SaveStatusCategories(byID) }()
		return StatusCategoriesResult{ByID: byID}
	}
}
