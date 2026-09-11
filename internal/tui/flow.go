package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/neilmpatterson/em-tui/internal/domain"
)

// flowComputer evaluates phase and cycle metrics from status-transition data.
// catByID maps Jira status IDs to category names (from /rest/api/3/status).
// phases maps status names to phase keys from TeamConfig.EffectivePhases().
type flowComputer struct {
	catByID map[string]string
	phases  map[string]string
}

func (fc flowComputer) category(id, name string) string {
	if c, ok := fc.catByID[id]; ok {
		return c
	}
	return guessCategory(name)
}

func (fc flowComputer) phaseOf(id, name string) domain.Phase {
	switch fc.category(id, name) {
	case domain.CatDone:
		return domain.PhaseDone
	case domain.CatTodo:
		return domain.PhaseTodo
	}
	switch fc.phases[strings.TrimSpace(name)] {
	case "dev":
		return domain.PhaseDev
	case "review":
		return domain.PhaseReview
	case "awaiting_merge":
		return domain.PhaseAwaitingMerge
	case "qa":
		return domain.PhaseQA
	case "awaiting_qa":
		return domain.PhaseAwaitingQA
	case "rework":
		return domain.PhaseRework
	case "blocked":
		return domain.PhaseBlocked
	}
	return phaseByKeyword(name)
}

func (fc flowComputer) isReopen(t domain.StatusTransition) bool {
	return fc.category(t.FromID, t.From) == domain.CatDone &&
		fc.category(t.ToID, t.To) != domain.CatDone
}

func (fc flowComputer) isQABounce(t domain.StatusTransition) bool {
	from := fc.phaseOf(t.FromID, t.From)
	to := fc.phaseOf(t.ToID, t.To)
	fromLate := from == domain.PhaseQA || from == domain.PhaseAwaitingQA || from == domain.PhaseReview
	toBack := to == domain.PhaseDev || to == domain.PhaseRework
	return fromLate && toBack
}

func (fc flowComputer) issueCycle(cl domain.IssueChangelog, open bool, now time.Time) (domain.IssueCycle, []string) {
	c := domain.IssueCycle{Key: cl.Key, Phases: domain.PhaseDurations{}}
	tr := cl.Transitions
	var unknown []string
	note := func(name string, p domain.Phase) {
		if p == domain.PhaseUnknown && strings.TrimSpace(name) != "" {
			unknown = append(unknown, name)
		}
	}
	if len(tr) == 0 {
		return c, unknown
	}

	if !cl.Created.IsZero() {
		p := domain.PhaseTodo
		if strings.TrimSpace(tr[0].From) != "" {
			p = fc.phaseOf(tr[0].FromID, tr[0].From)
			note(tr[0].From, p)
		}
		c.Phases.Add(p, tr[0].Timestamp.Sub(cl.Created).Hours()/24)
	}

	for i := range tr {
		p := fc.phaseOf(tr[i].ToID, tr[i].To)
		note(tr[i].To, p)

		if c.Start.IsZero() && p.IsWorking() {
			c.Start = tr[i].Timestamp
		}
		switch {
		case p == domain.PhaseDone && c.Done.IsZero():
			c.Done = tr[i].Timestamp
			c.Completed = true
		case p != domain.PhaseDone && !c.Done.IsZero():
			c.Done = time.Time{}
			c.Completed = false
		}
		if fc.isReopen(tr[i]) {
			c.Reopened++
		}
		if fc.isQABounce(tr[i]) {
			c.QABounces++
		}

		var until time.Time
		switch {
		case i+1 < len(tr):
			until = tr[i+1].Timestamp
		case open && p != domain.PhaseDone:
			until = now
		default:
			continue
		}
		c.Phases.Add(p, until.Sub(tr[i].Timestamp).Hours()/24)
	}

	if c.Completed && !c.Start.IsZero() && c.Done.After(c.Start) {
		c.CycleDays = c.Done.Sub(c.Start).Hours() / 24
	}
	return c, unknown
}

func (fc flowComputer) phasePasses(cl domain.IssueChangelog, want domain.Phase) []float64 {
	var out []float64
	tr := cl.Transitions
	for i := 0; i+1 < len(tr); i++ {
		if fc.phaseOf(tr[i].ToID, tr[i].To) != want {
			continue
		}
		if d := tr[i+1].Timestamp.Sub(tr[i].Timestamp).Hours() / 24; d > 0 {
			out = append(out, d)
		}
	}
	return out
}

// computeFlow folds the changelog batch into flow metrics. openKeys marks issues
// still in flight: they contribute to rework counts but are excluded from
// cycle-time percentiles and throughput, where an unfinished issue has no end.
func (fc flowComputer) computeFlow(changelogs []domain.IssueChangelog, openKeys map[string]bool,
	from, to time.Time) domain.MemberFlow {

	f := domain.MemberFlow{AvgPhase: domain.PhaseDurations{}, PhaseShare: domain.PhaseDurations{}}
	phaseSum := domain.PhaseDurations{}
	unknownSeen := map[string]bool{}
	var cycles []domain.IssueCycle

	for _, cl := range changelogs {
		open := openKeys[cl.Key]
		c, unknown := fc.issueCycle(cl, open, to)
		for _, s := range unknown {
			unknownSeen[s] = true
		}
		if cl.Truncated {
			f.Truncated++
		}

		if c.Reopened > 0 {
			f.ReopenedIssues++
			f.ReopenedEvents += c.Reopened
		}
		if c.QABounces > 0 {
			f.QABounceIssues++
			f.QABounceEvents += c.QABounces
		}
		f.ReviewDays = append(f.ReviewDays, fc.phasePasses(cl, domain.PhaseReview)...)
		f.Issues = append(f.Issues, c)

		if open {
			continue
		}
		f.IssuesAnalyzed++
		if c.CycleDays <= 0 {
			continue
		}
		f.IssuesCompleted++
		cycles = append(cycles, c)
		f.CycleDays = append(f.CycleDays, c.CycleDays)
		phaseSum.Merge(c.Phases)
		if c.CycleDays > f.MaxCycleDays {
			f.MaxCycleDays, f.MaxCycleKey = c.CycleDays, c.Key
		}
	}

	sort.Float64s(f.CycleDays)
	f.P50CycleDays = percentile(f.CycleDays, 50)
	f.P90CycleDays = percentile(f.CycleDays, 90)
	f.P50ReviewDays = percentile(f.ReviewDays, 50)

	if f.IssuesCompleted > 0 {
		f.AvgPhase = phaseSum.Scale(1 / float64(f.IssuesCompleted))
	}
	if total := phaseSum.Total(); total > 0 {
		f.PhaseShare = phaseSum.Scale(1 / total)
	}
	f.DoneByWeek, f.WindowWeeks = doneByWeek(cycles, from, to)
	if f.WindowWeeks > 0 {
		f.ThroughputPerWeek = float64(len(cycles)) / f.WindowWeeks
	}
	for s := range unknownSeen {
		f.UnknownStatuses = append(f.UnknownStatuses, s)
	}
	sort.Strings(f.UnknownStatuses)
	return f
}

// percentile returns the p-th percentile (0..100) by nearest rank. vals must
// already be sorted ascending.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := vals
	if !sort.Float64sAreSorted(s) {
		s = append([]float64(nil), vals...)
		sort.Float64s(s)
	}
	idx := int(math.Ceil(p/100*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

// doneByWeek buckets completed cycles into ISO weeks, most recent first, and
// returns the window length in weeks.
func doneByWeek(cycles []domain.IssueCycle, from, to time.Time) ([]domain.WeekCount, float64) {
	weeks := to.Sub(from).Hours() / 24 / 7
	if weeks < 1 {
		weeks = 1
	}
	counts := map[string]int{}
	for _, c := range cycles {
		if c.Done.IsZero() {
			continue
		}
		y, w := c.Done.ISOWeek()
		counts[fmt.Sprintf("%d-W%02d", y, w)]++
	}
	out := make([]domain.WeekCount, 0, len(counts))
	for k, v := range counts {
		out = append(out, domain.WeekCount{Label: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label > out[j].Label })
	return out, weeks
}
