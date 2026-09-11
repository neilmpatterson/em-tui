package jira

import (
	"testing"

	"github.com/neilmpatterson/em-tui/internal/domain"
)

// --- truncateDate ---

func TestTruncateDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"2026-01", "2026-01"},   // len < 10: unchanged
		{"2026-01-15", "2026-01-15"},                // exactly 10
		{"2026-01-15T10:30:00.000+0000", "2026-01-15"}, // longer: truncated
	}
	for _, c := range cases {
		if got := truncateDate(c.in); got != c.want {
			t.Errorf("truncateDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- issueJSON.toDomain ---

func TestIssueJSONToDomain_NilAssignee(t *testing.T) {
	i := issueJSON{Key: "ACME-1"}
	i.Fields.Summary = "Test ticket"
	i.Fields.Status.Name = "Done"
	i.Fields.Status.StatusCategory.Name = "Done"
	i.Fields.Priority.Name = "Medium"
	i.Fields.IssueType.Name = "Story"
	// Assignee left nil
	got := i.toDomain()
	if got.Assignee != "" {
		t.Errorf("nil assignee → %q, want empty", got.Assignee)
	}
	if got.Key != "ACME-1" {
		t.Errorf("Key = %q, want ACME-1", got.Key)
	}
}

func TestIssueJSONToDomain_PopulatedAssignee(t *testing.T) {
	i := issueJSON{Key: "ACME-2"}
	name := "Alex Chen"
	i.Fields.Assignee = &struct {
		DisplayName string `json:"displayName"`
	}{DisplayName: name}
	i.Fields.Updated = "2026-01-15T10:00:00Z"
	i.Fields.Created = "2026-01-10T09:00:00Z"
	got := i.toDomain()
	if got.Assignee != name {
		t.Errorf("Assignee = %q, want %q", got.Assignee, name)
	}
	if got.Updated != "2026-01-15" {
		t.Errorf("Updated = %q, want 2026-01-15", got.Updated)
	}
}

// --- ghIssueJSON.toCompact ---

func ptr(v float64) *float64 { return &v }

func TestGhIssueJSONToCompact_SPFromCurrent(t *testing.T) {
	g := ghIssueJSON{Key: "ACME-10"}
	g.EstimateStatistic.StatFieldValue = &struct {
		Value *float64 `json:"value"`
	}{Value: ptr(3)}
	g.CurrentEstimateStatistic.StatFieldValue = &struct {
		Value *float64 `json:"value"`
	}{Value: ptr(5)}
	got := g.toCompact(nil)
	if got.InitialSP == nil || *got.InitialSP != 3 {
		t.Errorf("InitialSP = %v, want 3", got.InitialSP)
	}
	if got.FinalSP == nil || *got.FinalSP != 5 {
		t.Errorf("FinalSP = %v, want 5", got.FinalSP)
	}
}

func TestGhIssueJSONToCompact_SPFallsBackToMap(t *testing.T) {
	g := ghIssueJSON{Key: "ACME-11"}
	// Both nil — should fall back to spMap
	spMap := map[string]float64{"ACME-11": 8}
	got := g.toCompact(spMap)
	if got.FinalSP == nil || *got.FinalSP != 8 {
		t.Errorf("FinalSP = %v, want 8 from spMap", got.FinalSP)
	}
	if got.InitialSP != nil {
		t.Errorf("InitialSP should be nil when EstimateStatistic is absent")
	}
}

func TestGhIssueJSONToCompact_BothNilNoMap(t *testing.T) {
	g := ghIssueJSON{Key: "ACME-12"}
	got := g.toCompact(nil)
	if got.FinalSP != nil || got.InitialSP != nil {
		t.Error("both SP fields should be nil when no estimates and no spMap entry")
	}
}

// --- sumSP ---

func TestSumSP(t *testing.T) {
	p := func(v float64) *float64 { return &v }
	cases := []struct {
		a, b *float64
		want *float64
	}{
		{nil, nil, nil},
		{p(3), nil, p(3)},
		{nil, p(5), p(5)},
		{p(3), p(5), p(8)},
	}
	for _, c := range cases {
		got := sumSP(c.a, c.b)
		if c.want == nil {
			if got != nil {
				t.Errorf("sumSP(nil,nil) = %v, want nil", got)
			}
		} else {
			if got == nil || *got != *c.want {
				t.Errorf("sumSP result = %v, want %v", got, *c.want)
			}
		}
	}
}

// --- NewDemo() round-trips ---

func TestFetchActiveSprintDemo(t *testing.T) {
	client := NewDemo()
	msg := client.FetchActiveSprint(42)()
	result, ok := msg.(SprintResult)
	if !ok {
		t.Fatalf("got %T, want SprintResult", msg)
	}
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Sprint.ID != 9 {
		t.Errorf("sprint ID = %d, want 9", result.Sprint.ID)
	}
	if result.Sprint.State != "active" {
		t.Errorf("sprint state = %q, want active", result.Sprint.State)
	}
}

func TestFetchRecentSprintsDemo(t *testing.T) {
	client := NewDemo()
	msg := client.FetchRecentSprints(42, 8)()
	result, ok := msg.(SprintsResult)
	if !ok {
		t.Fatalf("got %T, want SprintsResult", msg)
	}
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Sprints) == 0 {
		t.Error("got 0 sprints, want at least 1")
	}
}

func TestFetchSprintReportDemo(t *testing.T) {
	client := NewDemo()
	sprint := domain.Sprint{ID: 8, Name: "Platform Q4.1", State: "closed"}
	msg := client.FetchSprintReport(42, sprint)()
	result, ok := msg.(SprintReportResult)
	if !ok {
		t.Fatalf("got %T, want SprintReportResult", msg)
	}
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if result.Data.CompletedSP == nil || *result.Data.CompletedSP <= 0 {
		t.Error("CompletedSP should be > 0 for a closed sprint")
	}
	if len(result.Data.Completed) == 0 {
		t.Error("expected at least one completed issue in sprint report")
	}
}

func TestFetchStatusCategoriesDemo(t *testing.T) {
	client := NewDemo()
	msg := client.FetchStatusCategories()()
	result, ok := msg.(StatusCategoriesResult)
	if !ok {
		t.Fatalf("got %T, want StatusCategoriesResult", msg)
	}
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.ByID) == 0 {
		t.Error("expected non-empty status categories map")
	}
}
