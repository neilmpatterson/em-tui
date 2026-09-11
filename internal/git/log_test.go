package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	home, _ := os.UserHomeDir()

	cases := []struct {
		in   string
		want string
	}{
		{"~/foo", filepath.Join(home, "foo")},
		{"~/a/b/c", filepath.Join(home, "a/b/c")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"no-slash", "no-slash"},
	}
	for _, c := range cases {
		if got := expandTilde(c.in); got != c.want {
			t.Errorf("expandTilde(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDirKey(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"engines/calendar_manager/foo.rb", "engines/calendar_manager"},
		{"engines/calendar_manager/deep/nested.rb", "engines/calendar_manager"},
		{"api/controllers", "api"},       // 2 segments → first only
		{"README.md", "README.md"},        // 1 segment → itself
		{"a/b/c/d/e", "a/b"},
	}
	for _, c := range cases {
		if got := dirKey(c.path); got != c.want {
			t.Errorf("dirKey(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
