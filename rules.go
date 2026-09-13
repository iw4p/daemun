package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Rule is one policy statement. The webhook has no opinion about what the rules
// are — it only knows how to evaluate whatever it was handed.
type Rule struct {
	// RequireLabel names a label every Pod must carry.
	RequireLabel string `json:"requireLabel"`
	// Message replaces the default rejection text. Optional.
	Message string `json:"message,omitempty"`
}

// RuleSet is the whole policy as loaded from disk.
type RuleSet struct {
	Rules []Rule `json:"rules"`

	// source is the raw JSON, kept so a reload can tell a real change from a
	// re-read of identical bytes.
	source string
}

// Evaluate returns the first violation, or "" if the labels satisfy every rule.
// It knows nothing about Pods, HTTP or admission — which is what makes it the
// one piece worth unit testing.
func (rs *RuleSet) Evaluate(labels map[string]string) string {
	if rs == nil {
		return ""
	}
	for _, rule := range rs.Rules {
		if rule.RequireLabel == "" {
			continue
		}
		if _, ok := labels[rule.RequireLabel]; ok {
			continue
		}
		if rule.Message != "" {
			return rule.Message
		}
		return fmt.Sprintf("missing required label %q", rule.RequireLabel)
	}
	return ""
}

func (rs *RuleSet) describe() string {
	if rs == nil || len(rs.Rules) == 0 {
		return "no rules — everything is allowed"
	}
	names := make([]string, 0, len(rs.Rules))
	for _, r := range rs.Rules {
		names = append(names, r.RequireLabel)
	}
	return fmt.Sprintf("%d rule(s): %s", len(rs.Rules), strings.Join(names, ", "))
}

func loadRules(path string) (*RuleSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rs RuleSet
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("%s is not valid rule JSON: %w", path, err)
	}
	rs.source = string(raw)
	return &rs, nil
}

// watchRules re-reads the file on an interval.
//
// The kubelet updates a mounted ConfigMap by swapping a symlink, so re-reading
// the same path is all it takes to pick up a `kubectl apply` — no restart, no
// rebuild. Propagation takes up to a minute, which is why the interval is not
// aggressive.
//
// A bad file keeps the last good rules rather than dropping policy on the
// floor. Startup is stricter: main refuses to run without a readable file,
// because starting with no policy is silently permitting everything.
func watchRules(ctx context.Context, path string, every time.Duration, into *atomic.Pointer[RuleSet]) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		next, err := loadRules(path)
		if err != nil {
			log.Printf("could not reload rules, keeping the current ones: %v", err)
			continue
		}
		if current := into.Load(); current != nil && current.source == next.source {
			continue
		}
		into.Store(next)
		log.Printf("rules reloaded — %s", next.describe())
	}
}
