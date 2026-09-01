package config

import "testing"

func TestPhasesSurviveSave(t *testing.T) {
	c := &Config{Teams: []TeamConfig{{
		Name: "CMS", BoardID: 1, BoardType: "scrum",
		Phases: map[string]string{"Bug Fix Needed": "rework", "Ready to Merge": "awaiting_merge"},
	}}}
	out := marshalYAML(c)
	for _, want := range []string{"phases:", `"Bug Fix Needed": "rework"`, `"Ready to Merge": "awaiting_merge"`} {
		if !contains(out, want) {
			t.Errorf("marshalYAML dropped %q\n---\n%s", want, out)
		}
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
