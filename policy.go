package main

import (
	"fmt"
	"slices"
	"strings"
)

type Action string

const (
	ActionDeny Action = "deny"
	ActionWarn Action = "warn"
)

type Policy struct {
	Rules []Rule `json:"rules"`
}

type Rule struct {
	Name string `json:"name"`
	// Action defaults to deny. A warn rule lets the write through and attaches
	// its message to the kubectl output instead.
	Action  Action  `json:"action,omitempty"`
	Match   Match   `json:"match,omitempty"`
	Require Require `json:"require,omitempty"`
	Forbid  Forbid  `json:"forbid,omitempty"`
}

// Match narrows a rule. Empty fields mean "anything".
type Match struct {
	Kinds      []string `json:"kinds,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

type Require struct {
	Labels      []string `json:"labels,omitempty"`
	Annotations []string `json:"annotations,omitempty"`
}

type Forbid struct {
	LatestImageTag bool `json:"latestImageTag,omitempty"`
	// LabelValues maps a label to the values that are not allowed for it.
	LabelValues map[string][]string `json:"labelValues,omitempty"`
}

// Result is every violation found, split by what should happen about it.
type Result struct {
	Deny []string
	Warn []string
}

func (r Result) Allowed() bool { return len(r.Deny) == 0 }

func (p *Policy) Evaluate(o Object) Result {
	var res Result
	if p == nil {
		return res
	}
	for _, rule := range p.Rules {
		if !rule.applies(o) {
			continue
		}
		found := rule.violations(o)
		if len(found) == 0 {
			continue
		}
		labelled := make([]string, len(found))
		for i, v := range found {
			labelled[i] = rule.Name + ": " + v
		}
		if rule.Action == ActionWarn {
			res.Warn = append(res.Warn, labelled...)
		} else {
			res.Deny = append(res.Deny, labelled...)
		}
	}
	return res
}

func (r Rule) applies(o Object) bool {
	return matches(r.Match.Kinds, o.Kind) && matches(r.Match.Namespaces, o.Namespace)
}

func (r Rule) violations(o Object) []string {
	var out []string
	out = append(out, missing("label", o.Labels, r.Require.Labels)...)
	out = append(out, missing("annotation", o.Annotations, r.Require.Annotations)...)
	out = append(out, forbiddenValues(o.Labels, r.Forbid.LabelValues)...)
	if r.Forbid.LatestImageTag {
		out = append(out, latestTags(o.Images)...)
	}
	return out
}

// Validate rejects a policy that cannot do anything, so a typo in the ConfigMap
// fails loudly at load instead of silently enforcing nothing.
func (p *Policy) Validate() error {
	if len(p.Rules) == 0 {
		return fmt.Errorf("policy has no rules")
	}
	seen := map[string]bool{}
	for i, r := range p.Rules {
		switch {
		case r.Name == "":
			return fmt.Errorf("rule %d has no name", i)
		case seen[r.Name]:
			return fmt.Errorf("duplicate rule name %q", r.Name)
		case r.Action != "" && r.Action != ActionDeny && r.Action != ActionWarn:
			return fmt.Errorf("rule %q: action must be deny or warn, got %q", r.Name, r.Action)
		case !r.hasConstraints():
			return fmt.Errorf("rule %q has no constraints", r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

func (p *Policy) describe() string {
	names := make([]string, len(p.Rules))
	for i, r := range p.Rules {
		names[i] = r.Name
	}
	return fmt.Sprintf("%d rule(s): %s", len(names), strings.Join(names, ", "))
}

func matches(allowed []string, value string) bool {
	return len(allowed) == 0 || slices.Contains(allowed, value)
}

func missing(what string, have map[string]string, want []string) []string {
	var out []string
	for _, key := range want {
		if _, ok := have[key]; !ok {
			out = append(out, fmt.Sprintf("missing %s %q", what, key))
		}
	}
	return out
}

func forbiddenValues(labels map[string]string, forbidden map[string][]string) []string {
	var out []string
	for key, values := range forbidden {
		if got, ok := labels[key]; ok && slices.Contains(values, got) {
			out = append(out, fmt.Sprintf("label %s=%s is not allowed", key, got))
		}
	}
	return out
}

func (r Rule) hasConstraints() bool {
	return len(r.Require.Labels) > 0 ||
		len(r.Require.Annotations) > 0 ||
		len(r.Forbid.LabelValues) > 0 ||
		r.Forbid.LatestImageTag
}

func latestTags(images []string) []string {
	var out []string
	for _, image := range images {
		if strings.Contains(image, "@") { // pinned by digest
			continue
		}
		// Only the last path segment can carry a tag; earlier colons are ports.
		_, tag, tagged := strings.Cut(image[strings.LastIndex(image, "/")+1:], ":")
		if !tagged || tag == "latest" {
			out = append(out, fmt.Sprintf("image %q is not pinned to a version", image))
		}
	}
	return out
}
