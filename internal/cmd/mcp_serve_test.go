package cmd

import (
	"strings"
	"testing"
)

func TestMCPServeNeedsAnExplicitIdentityAndAgentsCannotServeAsPeople(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name, as string
		getenv   func(string) string
		want     string // "" means an error containing wantErr
		wantErr  string
	}{
		{"undeclared", "", env(), "", "needs an explicit identity"},
		{"flag human", "human", env(), "human", ""},
		{"flag agent", "agent", env(), "agent", ""},
		{"env human", "", env("ACLINE_ACTOR_TYPE", "human"), "human", ""},
		{"env agent", "", env("ACLINE_ACTOR_TYPE", "agent"), "agent", ""},
		{"model implies agent", "", env("ACLINE_MODEL", "claude-opus-5-5"), "agent", ""},
		{"matching flag", "agent", env("ACLINE_ACTOR_TYPE", "agent"), "agent", ""},
		{"person may serve as agent", "agent", env("ACLINE_ACTOR_TYPE", "human"), "agent", ""},
		{"agent env refuses --as human", "human", env("ACLINE_ACTOR_TYPE", "agent"), "", "cannot serve as a person"},
		{"model env refuses --as human", "human", env("ACLINE_MODEL", "claude-opus-5-5"), "", "cannot serve as a person"},
		{"bad flag", "robot", env(), "", "must be human or agent"},
	}
	for _, c := range cases {
		got, err := mcpServeActorType(c.as, c.getenv)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: got (%q, %v), want an error containing %q", c.name, got, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got (%q, %v), want %q", c.name, got, err, c.want)
		}
	}
}
