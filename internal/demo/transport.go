package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Transport implements http.RoundTripper with pre-baked fake Acme Corp data.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	query := req.URL.Query()

	switch {
	case path == "/rest/api/3/status":
		return ok(statusCatsPayload())
	case strings.HasPrefix(path, "/rest/api/3/issue/"):
		key := strings.TrimPrefix(path, "/rest/api/3/issue/")
		return ok(changelogPayload(key))
	case path == "/rest/api/3/search/jql":
		return ok(historyPayload())
	case path == "/rest/greenhopper/1.0/rapid/charts/sprintreport":
		id, _ := strconv.Atoi(query.Get("sprintId"))
		return ok(sprintReportPayload(id))
	case strings.HasPrefix(path, "/rest/agile/") && strings.HasSuffix(path, "/issue"):
		spID := sprintIDFromPath(path)
		if query.Get("fields") == "story_points" {
			return ok(map[string]any{"total": 0, "issues": []any{}})
		}
		return ok(boardIssuesPayload(spID))
	case strings.HasPrefix(path, "/rest/agile/1.0/board/42/sprint"):
		if query.Get("state") == "active" {
			return ok(activeSprintPayload())
		}
		return ok(closedSprintsPayload())
	default:
		return ok(map[string]any{})
	}
}

func ok(body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header:     make(http.Header),
	}, nil
}

func sprintIDFromPath(path string) int {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "sprint" && i+1 < len(parts) {
			id, _ := strconv.Atoi(parts[i+1])
			return id
		}
	}
	return 0
}

// --- Status categories ---

func statusCatsPayload() any {
	return []map[string]any{
		{"id": "10001", "statusCategory": map[string]any{"name": "To Do"}},
		{"id": "10002", "statusCategory": map[string]any{"name": "In Progress"}},
		{"id": "10003", "statusCategory": map[string]any{"name": "In Progress"}},
		{"id": "10004", "statusCategory": map[string]any{"name": "In Progress"}},
		{"id": "10005", "statusCategory": map[string]any{"name": "Done"}},
		{"id": "10006", "statusCategory": map[string]any{"name": "In Progress"}},
	}
}

// --- Sprints ---

func activeSprintPayload() any {
	return map[string]any{
		"values": []map[string]any{
			{"id": 9, "name": "Platform Q4.1", "state": "active", "startDate": "2026-08-25", "endDate": "2026-09-05"},
		},
		"isLast": true,
	}
}

func closedSprintsPayload() any {
	return map[string]any{
		"values": []map[string]any{
			{"id": 8, "name": "Platform Q3.4", "state": "closed", "startDate": "2026-08-11", "endDate": "2026-08-22"},
			{"id": 7, "name": "Platform Q3.3", "state": "closed", "startDate": "2026-07-28", "endDate": "2026-08-08"},
			{"id": 6, "name": "Platform Q3.2", "state": "closed", "startDate": "2026-07-14", "endDate": "2026-07-25"},
			{"id": 5, "name": "Platform Q3.1", "state": "closed", "startDate": "2026-06-30", "endDate": "2026-07-11"},
			{"id": 4, "name": "Platform Q2.4", "state": "closed", "startDate": "2026-06-16", "endDate": "2026-06-27"},
			{"id": 3, "name": "Platform Q2.3", "state": "closed", "startDate": "2026-06-02", "endDate": "2026-06-13"},
			{"id": 2, "name": "Platform Q2.2", "state": "closed", "startDate": "2026-05-19", "endDate": "2026-05-30"},
			{"id": 1, "name": "Platform Q2.1", "state": "closed", "startDate": "2026-05-05", "endDate": "2026-05-16"},
		},
		"isLast": true,
	}
}

// --- Sprint reports ---

type sprintIssue struct {
	key      string
	summary  string
	assignee string
	status   string
	priority string
	sp       float64
}

type sprintDef struct {
	completed   []sprintIssue
	notComplete []sprintIssue
}

var sprints = map[int]sprintDef{
	1: {
		completed: []sprintIssue{
			{"PLAT-101", "Implement rate limiting for public API", "Alex Chen", "Done", "High", 5},
			{"PLAT-102", "Fix N+1 query in permissions endpoint", "Sam Rivera", "Done", "Medium", 3},
			{"PLAT-103", "Upgrade PostgreSQL to version 16", "Jordan Lee", "Done", "Medium", 2},
			{"PLAT-104", "Add webhook delivery retry logic", "Casey Kim", "Done", "High", 5},
			{"PLAT-105", "Fix CORS preflight rejection for custom headers", "Alex Chen", "Done", "Medium", 3},
		},
		notComplete: []sprintIssue{
			{"PLAT-106", "Build audit log export functionality", "Sam Rivera", "Code Review", "High", 5},
		},
	},
	2: {
		completed: []sprintIssue{
			{"PLAT-106", "Build audit log export functionality", "Sam Rivera", "Done", "High", 5},
			{"PLAT-107", "Implement SCIM user provisioning", "Jordan Lee", "Done", "High", 8},
			{"PLAT-108", "Fix memory leak in background workers", "Casey Kim", "Done", "Critical", 3},
			{"PLAT-109", "Add API key rotation workflow", "Alex Chen", "Done", "Medium", 3},
			{"PLAT-110", "Fix timezone handling in job scheduler", "Sam Rivera", "Done", "Medium", 2},
		},
		notComplete: []sprintIssue{
			{"PLAT-111", "Build GraphQL subscription support", "Jordan Lee", "In Progress", "High", 8},
		},
	},
	3: {
		completed: []sprintIssue{
			{"PLAT-111", "Build GraphQL subscription support", "Jordan Lee", "Done", "High", 8},
			{"PLAT-112", "Implement real-time event streaming endpoint", "Casey Kim", "Done", "High", 5},
			{"PLAT-113", "Resolve deadlock in webhook processing", "Alex Chen", "Done", "Critical", 3},
			{"PLAT-114", "Add distributed tracing support", "Sam Rivera", "Done", "Medium", 5},
			{"PLAT-115", "Fix incorrect HTTP 200 returned on failed writes", "Jordan Lee", "Done", "High", 2},
			{"PLAT-116", "Implement idempotency keys for POST requests", "Casey Kim", "Done", "Medium", 5},
		},
		notComplete: []sprintIssue{
			{"PLAT-117", "Build feature flag service", "Alex Chen", "Testing", "Medium", 5},
			{"PLAT-118", "Fix retry storm when downstream returns 503", "Sam Rivera", "Code Review", "High", 3},
		},
	},
	4: {
		completed: []sprintIssue{
			{"PLAT-117", "Build feature flag service", "Alex Chen", "Done", "Medium", 5},
			{"PLAT-118", "Fix retry storm when downstream returns 503", "Sam Rivera", "Done", "High", 3},
			{"PLAT-119", "Add API usage analytics dashboard", "Jordan Lee", "Done", "Medium", 5},
			{"PLAT-120", "Implement OpenAPI spec generation", "Casey Kim", "Done", "Medium", 3},
			{"PLAT-121", "Fix cache invalidation on role updates", "Alex Chen", "Done", "High", 2},
		},
		notComplete: []sprintIssue{
			{"PLAT-122", "Build background job queue system", "Sam Rivera", "In Progress", "High", 8},
		},
	},
	5: {
		completed: []sprintIssue{
			{"PLAT-122", "Build background job queue system", "Sam Rivera", "Done", "High", 8},
			{"PLAT-123", "Implement caching layer for read-heavy endpoints", "Jordan Lee", "Done", "Medium", 5},
			{"PLAT-124", "Fix audit log missing entries on bulk operations", "Casey Kim", "Done", "High", 3},
			{"PLAT-125", "Build search indexing pipeline", "Alex Chen", "Done", "Medium", 8},
			{"PLAT-126", "Resolve encoding issue in non-ASCII webhook payloads", "Sam Rivera", "Done", "Medium", 2},
		},
		notComplete: []sprintIssue{
			{"PLAT-127", "Implement SSO with SAML 2.0", "Jordan Lee", "Code Review", "Critical", 8},
		},
	},
	6: {
		completed: []sprintIssue{
			{"PLAT-127", "Implement SSO with SAML 2.0", "Jordan Lee", "Done", "Critical", 8},
			{"PLAT-128", "Build permission inheritance model", "Casey Kim", "Done", "High", 5},
			{"PLAT-129", "Fix race condition in session store cleanup", "Alex Chen", "Done", "High", 3},
			{"PLAT-130", "Add multi-tenant data isolation layer", "Sam Rivera", "Done", "Critical", 8},
			{"PLAT-131", "Fix double-counting in API usage metrics", "Jordan Lee", "Done", "Medium", 2},
		},
		notComplete: []sprintIssue{
			{"PLAT-132", "Implement A/B testing framework", "Casey Kim", "In Progress", "Medium", 5},
		},
	},
	7: {
		completed: []sprintIssue{
			{"PLAT-132", "Implement A/B testing framework", "Casey Kim", "Done", "Medium", 5},
			{"PLAT-133", "Add canary deployment support", "Alex Chen", "Done", "High", 8},
			{"PLAT-134", "Resolve 504 timeout on large export requests", "Sam Rivera", "Done", "High", 3},
			{"PLAT-135", "Build developer portal SDK documentation", "Jordan Lee", "Done", "Medium", 5},
			{"PLAT-136", "Fix webhook secret validation bypass on retry", "Casey Kim", "Done", "Critical", 3},
			{"PLAT-137", "Configure database connection pooling", "Alex Chen", "Done", "Medium", 3},
		},
		notComplete: []sprintIssue{
			{"PLAT-138", "Implement service mesh integration", "Sam Rivera", "Testing", "High", 8},
		},
	},
	8: {
		completed: []sprintIssue{
			{"PLAT-138", "Implement service mesh integration", "Sam Rivera", "Done", "High", 8},
			{"PLAT-139", "Fix permission check bypass in batch operations", "Jordan Lee", "Done", "Critical", 5},
			{"PLAT-140", "Build internal developer platform CLI", "Casey Kim", "Done", "Medium", 8},
			{"PLAT-141", "Add email notification service", "Alex Chen", "Done", "Medium", 5},
			{"PLAT-142", "Fix connection leak in database pool on timeout", "Sam Rivera", "Done", "High", 3},
		},
		notComplete: []sprintIssue{
			{"PLAT-143", "Implement API versioning strategy", "Jordan Lee", "Code Review", "High", 8},
		},
	},
	9: { // active sprint
		completed: []sprintIssue{
			{"PLAT-143", "Implement API versioning strategy", "Jordan Lee", "Done", "High", 8},
			{"PLAT-144", "Fix slow query after statistics update", "Casey Kim", "Done", "High", 3},
		},
		notComplete: []sprintIssue{
			{"PLAT-145", "Add file upload with virus scanning", "Alex Chen", "In Progress", "Medium", 5},
			{"PLAT-146", "Implement PDF report generation", "Sam Rivera", "Code Review", "Medium", 3},
			{"PLAT-147", "Fix missing index on heavily queried column", "Jordan Lee", "Testing", "High", 2},
		},
	},
}

func ghIssueMap(d sprintIssue) map[string]any {
	return map[string]any{
		"key":          d.key,
		"summary":      d.summary,
		"assigneeName": d.assignee,
		"statusName":   d.status,
		"priorityName": d.priority,
		"estimateStatistic": map[string]any{
			"statFieldValue": map[string]any{"value": d.sp},
		},
		"currentEstimateStatistic": map[string]any{
			"statFieldValue": map[string]any{"value": d.sp},
		},
	}
}

func sumSP(issues []sprintIssue) float64 {
	var total float64
	for _, i := range issues {
		total += i.sp
	}
	return total
}

func sprintReportPayload(sprintID int) any {
	data, found := sprints[sprintID]
	empty := map[string]any{"value": nil}
	if !found {
		return map[string]any{"contents": map[string]any{
			"completedIssues": []any{}, "issuesNotCompletedInCurrentSprint": []any{},
			"puntedIssues": []any{}, "issueKeysAddedDuringSprint": map[string]any{},
			"completedIssuesInitialEstimateSum": empty, "completedIssueEstimateSum": empty,
			"incompletedIssuesInitialEstimateSum": empty, "incompletedIssuesEstimateSum": empty,
			"allIssuesEstimateSum": empty,
		}}
	}
	completed := make([]map[string]any, 0, len(data.completed))
	for _, d := range data.completed {
		completed = append(completed, ghIssueMap(d))
	}
	notComplete := make([]map[string]any, 0, len(data.notComplete))
	for _, d := range data.notComplete {
		notComplete = append(notComplete, ghIssueMap(d))
	}
	cSP := sumSP(data.completed)
	iSP := sumSP(data.notComplete)
	return map[string]any{"contents": map[string]any{
		"completedIssues":                    completed,
		"issuesNotCompletedInCurrentSprint":  notComplete,
		"puntedIssues":                       []any{},
		"issueKeysAddedDuringSprint":          map[string]any{},
		"completedIssuesInitialEstimateSum":   map[string]any{"value": cSP},
		"completedIssueEstimateSum":           map[string]any{"value": cSP},
		"incompletedIssuesInitialEstimateSum": map[string]any{"value": iSP},
		"incompletedIssuesEstimateSum":        map[string]any{"value": iSP},
		"allIssuesEstimateSum":                map[string]any{"value": cSP + iSP},
	}}
}

func boardIssuesPayload(sprintID int) any {
	data, found := sprints[sprintID]
	if !found {
		return map[string]any{"total": 0, "issues": []any{}}
	}
	var all []sprintIssue
	all = append(all, data.completed...)
	all = append(all, data.notComplete...)

	issues := make([]map[string]any, 0, len(all))
	for _, d := range all {
		catName := "In Progress"
		if d.status == "Done" {
			catName = "Done"
		}
		issues = append(issues, map[string]any{
			"key": d.key,
			"fields": map[string]any{
				"summary": d.summary,
				"status": map[string]any{
					"name":           d.status,
					"statusCategory": map[string]any{"name": catName},
				},
				"priority":  map[string]any{"name": d.priority},
				"issuetype": map[string]any{"name": "Story"},
				"assignee":  map[string]any{"displayName": d.assignee},
				"updated":   "2026-09-01",
				"created":   "2026-08-25",
			},
		})
	}
	return map[string]any{"total": len(issues), "issues": issues}
}

// --- History issues (JQL search for 18-month stats) ---

type historyItem struct {
	summary   string
	issueType string
	priority  string
}

// 80 issues for Alex Chen: 30 Stories, 35 Bugs, 15 Tasks
var historyItems = []historyItem{
	// Stories
	{"Add OAuth2 refresh token support", "Story", "High"},
	{"Implement rate limiting for API endpoints", "Story", "High"},
	{"Build GraphQL subscription support", "Story", "High"},
	{"Add multi-tenant data isolation layer", "Story", "Critical"},
	{"Implement API versioning strategy", "Story", "High"},
	{"Build developer portal SDK", "Story", "Medium"},
	{"Add webhook delivery retry logic", "Story", "High"},
	{"Build audit log export functionality", "Story", "High"},
	{"Implement SCIM user provisioning", "Story", "High"},
	{"Add API key rotation workflow", "Story", "Medium"},
	{"Build real-time event streaming endpoint", "Story", "High"},
	{"Implement idempotency keys for POST requests", "Story", "Medium"},
	{"Add API usage analytics dashboard", "Story", "Medium"},
	{"Implement OpenAPI spec generation", "Story", "Medium"},
	{"Build background job queue system", "Story", "High"},
	{"Add distributed tracing support", "Story", "Medium"},
	{"Implement feature flag service", "Story", "High"},
	{"Build A/B testing framework", "Story", "Medium"},
	{"Add canary deployment support", "Story", "High"},
	{"Implement service mesh integration", "Story", "High"},
	{"Build internal developer platform CLI", "Story", "Medium"},
	{"Add database connection pooling", "Story", "Medium"},
	{"Implement caching layer for read-heavy endpoints", "Story", "Medium"},
	{"Build search indexing pipeline", "Story", "Medium"},
	{"Add email notification service", "Story", "Medium"},
	{"Implement PDF report generation", "Story", "Low"},
	{"Build CSV data export endpoint", "Story", "Medium"},
	{"Add file upload with virus scanning", "Story", "High"},
	{"Implement SSO with SAML 2.0", "Story", "Critical"},
	{"Build permission inheritance model", "Story", "High"},
	// Bugs
	{"Fix token expiry race condition in auth middleware", "Bug", "Critical"},
	{"Resolve N+1 query in user permissions endpoint", "Bug", "High"},
	{"Fix memory leak in long-running background workers", "Bug", "High"},
	{"Correct pagination offset for large datasets", "Bug", "Medium"},
	{"Fix timezone handling in scheduled job runner", "Bug", "Medium"},
	{"Resolve deadlock in concurrent webhook processing", "Bug", "Critical"},
	{"Fix incorrect HTTP 200 returned on failed writes", "Bug", "High"},
	{"Correct cache invalidation on role updates", "Bug", "High"},
	{"Fix audit log missing entries on bulk operations", "Bug", "High"},
	{"Resolve encoding issue in non-ASCII webhook payloads", "Bug", "Medium"},
	{"Fix race condition in session store cleanup", "Bug", "High"},
	{"Correct error message for invalid OAuth scopes", "Bug", "Medium"},
	{"Fix retry storm when downstream service returns 503", "Bug", "High"},
	{"Resolve stuck jobs in dead letter queue", "Bug", "High"},
	{"Fix CORS preflight rejection for custom headers", "Bug", "Medium"},
	{"Correct Content-Type header on file download endpoint", "Bug", "Medium"},
	{"Fix double-counting in API usage metrics", "Bug", "Medium"},
	{"Resolve 504 timeout on large export requests", "Bug", "High"},
	{"Fix stale cache after config reload", "Bug", "Medium"},
	{"Correct rate limit window boundary calculation", "Bug", "High"},
	{"Fix missing rollback on failed multi-step transaction", "Bug", "Critical"},
	{"Resolve inconsistent response for concurrent updates", "Bug", "High"},
	{"Fix webhook secret validation bypass on retry", "Bug", "Critical"},
	{"Correct index corruption after failed migration", "Bug", "Critical"},
	{"Fix memory pressure during peak traffic window", "Bug", "High"},
	{"Resolve permission check bypass in batch operations", "Bug", "Critical"},
	{"Fix incorrect status code for expired API tokens", "Bug", "High"},
	{"Correct log sampling rate under high load", "Bug", "Medium"},
	{"Fix connection leak in database pool on timeout", "Bug", "High"},
	{"Resolve slow query after statistics update", "Bug", "High"},
	{"Fix missing index on heavily queried column", "Bug", "High"},
	{"Correct trailing slash handling in URL router", "Bug", "Low"},
	{"Fix flaky test in integration suite", "Bug", "Medium"},
	{"Resolve build failure on dependency update", "Bug", "Medium"},
	{"Fix false positive in anomaly detection alert", "Bug", "Medium"},
	// Tasks
	{"Upgrade PostgreSQL to version 16", "Task", "Medium"},
	{"Rotate production API keys", "Task", "High"},
	{"Archive stale feature flags", "Task", "Low"},
	{"Update OpenAPI spec for v2 endpoints", "Task", "Medium"},
	{"Configure PagerDuty escalation policy", "Task", "Medium"},
	{"Add monitoring dashboards for new services", "Task", "Medium"},
	{"Document deployment runbook", "Task", "Medium"},
	{"Set up staging environment for new service", "Task", "Medium"},
	{"Configure rate limit thresholds in production", "Task", "Medium"},
	{"Update dependency audit report", "Task", "Medium"},
	{"Review and close stale GitHub issues", "Task", "Low"},
	{"Configure log retention policy", "Task", "Medium"},
	{"Update SSL certificate for API gateway", "Task", "High"},
	{"Archive deprecated v1 API endpoints", "Task", "Medium"},
	{"Set up automated database backups", "Task", "High"},
}

func historyPayload() any {
	base := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	issues := make([]map[string]any, 0, len(historyItems))
	for i, item := range historyItems {
		n := i + 1
		created := base.AddDate(0, 0, (n-1)*7).Format("2006-01-02")
		updated := base.AddDate(0, 0, (n-1)*7+5).Format("2006-01-02")
		issues = append(issues, map[string]any{
			"key": fmt.Sprintf("PLAT-%d", n),
			"fields": map[string]any{
				"summary": item.summary,
				"status": map[string]any{
					"name":           "Done",
					"statusCategory": map[string]any{"name": "Done"},
				},
				"priority":  map[string]any{"name": item.priority},
				"issuetype": map[string]any{"name": item.issueType},
				"assignee":  map[string]any{"displayName": "Alex Chen"},
				"updated":   updated,
				"created":   created,
			},
		})
	}
	return map[string]any{"issues": issues, "nextPageToken": ""}
}

// --- Changelogs ---

func changelogPayload(key string) any {
	var n int
	if parts := strings.SplitN(key, "-", 2); len(parts) == 2 {
		n, _ = strconv.Atoi(parts[1])
	}
	if n <= 0 {
		n = 1
	}

	// Date base: PLAT-1 starts 2025-01-15, each issue 7 days apart.
	// Sprint issues (N>100) use a different base so dates are still plausible.
	var baseDate time.Time
	if n <= 80 {
		baseDate = time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC).AddDate(0, 0, (n-1)*7)
	} else {
		baseDate = time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, (n-101)*14)
	}

	created := baseDate.Format("2006-01-02T10:00:00.000Z")

	var histories []map[string]any
	switch n % 4 {
	case 0: // standard: To Do → In Progress → Code Review → Done
		histories = []map[string]any{
			transition(baseDate.AddDate(0, 0, 1), "10001", "To Do", "10002", "In Progress"),
			transition(baseDate.AddDate(0, 0, 4), "10002", "In Progress", "10003", "Code Review"),
			transition(baseDate.AddDate(0, 0, 6), "10003", "Code Review", "10005", "Done"),
		}
	case 1: // rework: Testing → Bug Fix Needed → In Progress → Done
		histories = []map[string]any{
			transition(baseDate.AddDate(0, 0, 1), "10001", "To Do", "10002", "In Progress"),
			transition(baseDate.AddDate(0, 0, 3), "10002", "In Progress", "10004", "Testing"),
			transition(baseDate.AddDate(0, 0, 4), "10004", "Testing", "10006", "Bug Fix Needed"),
			transition(baseDate.AddDate(0, 0, 5), "10006", "Bug Fix Needed", "10002", "In Progress"),
			transition(baseDate.AddDate(0, 0, 7), "10002", "In Progress", "10005", "Done"),
		}
	case 2: // slow review: 18 days in Code Review
		histories = []map[string]any{
			transition(baseDate.AddDate(0, 0, 1), "10001", "To Do", "10002", "In Progress"),
			transition(baseDate.AddDate(0, 0, 3), "10002", "In Progress", "10003", "Code Review"),
			transition(baseDate.AddDate(0, 0, 21), "10003", "Code Review", "10005", "Done"),
		}
	default: // case 3: fast path
		histories = []map[string]any{
			transition(baseDate.AddDate(0, 0, 1), "10001", "To Do", "10002", "In Progress"),
			transition(baseDate.AddDate(0, 0, 3), "10002", "In Progress", "10005", "Done"),
		}
	}

	return map[string]any{
		"fields": map[string]any{"created": created},
		"changelog": map[string]any{
			"total":     len(histories),
			"histories": histories,
		},
	}
}

func transition(t time.Time, fromID, fromName, toID, toName string) map[string]any {
	return map[string]any{
		"created": t.Format("2006-01-02T10:00:00.000Z"),
		"items": []map[string]any{
			{
				"field":      "status",
				"from":       fromID,
				"to":         toID,
				"fromString": fromName,
				"toString":   toName,
			},
		},
	}
}
