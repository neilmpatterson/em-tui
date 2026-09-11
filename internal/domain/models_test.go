package domain

import (
	"testing"
	"time"
)

func TestParseJiraTime(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
		wantY   int
		wantM   time.Month
		wantD   int
	}{
		{
			name:  "with milliseconds and offset",
			input: "2026-01-15T10:30:00.000+0000",
			wantY: 2026, wantM: time.January, wantD: 15,
		},
		{
			name:  "without milliseconds with offset",
			input: "2026-03-22T14:00:00+0530",
			wantY: 2026, wantM: time.March, wantD: 22,
		},
		{
			name:  "RFC3339",
			input: "2025-11-01T08:00:00Z",
			wantY: 2025, wantM: time.November, wantD: 1,
		},
		{
			name:    "bad input",
			input:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseJiraTime(c.input)
			if c.wantErr {
				if err == nil {
					t.Errorf("ParseJiraTime(%q) = %v, want error", c.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseJiraTime(%q) error: %v", c.input, err)
			}
			// Compare in UTC so offset-aware inputs still match.
			utc := got.UTC()
			if utc.Year() != c.wantY || utc.Month() != c.wantM || utc.Day() != c.wantD {
				t.Errorf("ParseJiraTime(%q) = %v, want %04d-%02d-%02d",
					c.input, got, c.wantY, c.wantM, c.wantD)
			}
		})
	}
}

func TestPhaseIsWait(t *testing.T) {
	waitPhases := map[Phase]bool{
		PhaseUnknown:       false,
		PhaseTodo:          false,
		PhaseDev:           false,
		PhaseReview:        false,
		PhaseAwaitingMerge: true,
		PhaseQA:            false,
		PhaseAwaitingQA:    true,
		PhaseRework:        false,
		PhaseBlocked:       true,
		PhaseDone:          false,
	}
	for p, want := range waitPhases {
		if got := p.IsWait(); got != want {
			t.Errorf("Phase(%v).IsWait() = %v, want %v", p, got, want)
		}
	}
}

func TestPhaseIsWorking(t *testing.T) {
	workingPhases := map[Phase]bool{
		PhaseUnknown:       false,
		PhaseTodo:          false,
		PhaseDev:           true,
		PhaseReview:        true,
		PhaseAwaitingMerge: true,
		PhaseQA:            true,
		PhaseAwaitingQA:    true,
		PhaseRework:        true,
		PhaseBlocked:       true,
		PhaseDone:          false,
	}
	for p, want := range workingPhases {
		if got := p.IsWorking(); got != want {
			t.Errorf("Phase(%v).IsWorking() = %v, want %v", p, got, want)
		}
	}
}

func TestPhaseDurationsScale(t *testing.T) {
	d := PhaseDurations{PhaseDev: 4.0, PhaseReview: 2.0}
	got := d.Scale(0.5)
	if got[PhaseDev] != 2.0 {
		t.Errorf("Scale PhaseDev = %v, want 2.0", got[PhaseDev])
	}
	if got[PhaseReview] != 1.0 {
		t.Errorf("Scale PhaseReview = %v, want 1.0", got[PhaseReview])
	}
	// Original must not be mutated.
	if d[PhaseDev] != 4.0 {
		t.Error("Scale mutated the receiver")
	}
}

func TestPhaseDurationsMerge(t *testing.T) {
	a := PhaseDurations{PhaseDev: 3.0, PhaseReview: 1.0}
	b := PhaseDurations{PhaseDev: 2.0, PhaseQA: 4.0}
	a.Merge(b)
	if a[PhaseDev] != 5.0 {
		t.Errorf("Merge PhaseDev = %v, want 5.0", a[PhaseDev])
	}
	if a[PhaseReview] != 1.0 {
		t.Errorf("Merge PhaseReview = %v, want 1.0", a[PhaseReview])
	}
	if a[PhaseQA] != 4.0 {
		t.Errorf("Merge PhaseQA = %v, want 4.0", a[PhaseQA])
	}
}
