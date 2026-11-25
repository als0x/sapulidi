package config

import "testing"

func TestSettingsOptionalEnabledUnlessSetFalse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"no config", "", true},
		{"table without enabled", "[category.deps]\nage = \"30d\"\n", true},
		{"enabled true", "[category.deps]\nenabled = true\n", true},
		{"enabled false", "[category.deps]\nenabled = false\n", false},
	} {
		s := settingsFor(t, tc.body, nil)
		if got := s.CategoryEnabled("deps"); got != tc.want {
			t.Errorf("%s: optional enabled = %v, want %v", tc.name, got, tc.want)
		}
		if s.CategoryEnabled("models") || s.CategoryEnabled("undeclared") {
			t.Errorf("%s: never and undeclared categories stay off", tc.name)
		}
		if !s.CategoryEnabled("temp") {
			t.Errorf("%s: auto stays on", tc.name)
		}
	}
}
