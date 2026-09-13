package main

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Evaluate is the one piece worth testing: it has no HTTP, no TLS and no
// cluster in it. That is the point of pulling the policy out of the handler.
func TestEvaluate(t *testing.T) {
	rules := &RuleSet{Rules: []Rule{
		{RequireLabel: "team"},
		{RequireLabel: "cost-center", Message: "finance needs a cost-center label"},
	}}

	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "all labels present",
			labels: map[string]string{"team": "platform", "cost-center": "eng"},
			want:   "",
		},
		{
			name:   "no labels at all",
			labels: nil,
			want:   `missing required label "team"`,
		},
		{
			name:   "first rule passes, second fails with its custom message",
			labels: map[string]string{"team": "platform"},
			want:   "finance needs a cost-center label",
		},
		{
			name:   "an empty value still counts as present",
			labels: map[string]string{"team": "", "cost-center": ""},
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.Evaluate(tc.labels); got != tc.want {
				t.Errorf("Evaluate(%v) = %q, want %q", tc.labels, got, tc.want)
			}
		})
	}
}

func TestEvaluateEmptyPolicy(t *testing.T) {
	empty := &RuleSet{}
	if got := empty.Evaluate(nil); got != "" {
		t.Errorf("an empty rule set should allow everything, got %q", got)
	}
}

// watchRules is what makes the policy dynamic: editing the ConfigMap changes
// behaviour in a running pod, with no rebuild and no restart.
func TestWatchRulesPicksUpChanges(t *testing.T) {
	path := t.TempDir() + "/rules.json"
	if err := os.WriteFile(path, []byte(`{"rules":[{"requireLabel":"team"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	initial, err := loadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	var current atomic.Pointer[RuleSet]
	current.Store(initial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchRules(ctx, path, 20*time.Millisecond, &current)

	if got := current.Load().Evaluate(map[string]string{"team": "platform"}); got != "" {
		t.Fatalf("before the edit, a Pod with team should pass, got %q", got)
	}

	// Same thing `kubectl apply` on the ConfigMap eventually does to the mount.
	if err := os.WriteFile(path, []byte(`{"rules":[{"requireLabel":"owner"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	want := `missing required label "owner"`
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if current.Load().Evaluate(map[string]string{"team": "platform"}) == want {
			return // reloaded
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("rules never reloaded; still %q", current.Load().Evaluate(map[string]string{"team": "platform"}))
}
