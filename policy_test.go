package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	policy := &Policy{Rules: []Rule{
		{
			Name:    "ownership",
			Require: Require{Labels: []string{"team", "owner"}},
		},
		{
			Name:   "pinned-images",
			Match:  Match{Kinds: []string{"Pod"}},
			Forbid: Forbid{LatestImageTag: true},
		},
		{
			Name:    "no-prod-from-sandbox",
			Action:  ActionWarn,
			Forbid:  Forbid{LabelValues: map[string][]string{"env": {"prod"}}},
			Require: Require{Annotations: []string{"change-ticket"}},
		},
	}}

	tests := []struct {
		name     string
		obj      Object
		wantDeny []string
		wantWarn []string
	}{
		{
			name: "everything satisfied",
			obj: Object{
				Kind:        "Pod",
				Labels:      map[string]string{"team": "platform", "owner": "nima"},
				Annotations: map[string]string{"change-ticket": "OPS-1"},
				Images:      []string{"nginx:1.27"},
			},
		},
		{
			name:     "reports every missing label, not just the first",
			obj:      Object{Kind: "Pod", Labels: nil, Images: []string{"nginx:1.27"}, Annotations: map[string]string{"change-ticket": "OPS-1"}},
			wantDeny: []string{`ownership: missing label "team"`, `ownership: missing label "owner"`},
		},
		{
			name: "unpinned images",
			obj: Object{
				Kind:        "Pod",
				Labels:      map[string]string{"team": "platform", "owner": "nima"},
				Annotations: map[string]string{"change-ticket": "OPS-1"},
				Images:      []string{"nginx:latest", "redis"},
			},
			wantDeny: []string{
				`pinned-images: image "nginx:latest" is not pinned to a version`,
				`pinned-images: image "redis" is not pinned to a version`,
			},
		},
		{
			name: "a warn rule does not block",
			obj: Object{
				Kind:   "Pod",
				Labels: map[string]string{"team": "platform", "owner": "nima", "env": "prod"},
				Images: []string{"nginx:1.27"},
			},
			wantWarn: []string{
				`no-prod-from-sandbox: missing annotation "change-ticket"`,
				`no-prod-from-sandbox: label env=prod is not allowed`,
			},
		},
		{
			name: "kind match keeps the image rule off a Service",
			obj: Object{
				Kind:        "Service",
				Labels:      map[string]string{"team": "platform", "owner": "nima"},
				Annotations: map[string]string{"change-ticket": "OPS-1"},
				Images:      []string{"nginx:latest"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Evaluate(tc.obj)
			if !reflect.DeepEqual(got.Deny, tc.wantDeny) {
				t.Errorf("deny:\n got %q\nwant %q", got.Deny, tc.wantDeny)
			}
			if !reflect.DeepEqual(got.Warn, tc.wantWarn) {
				t.Errorf("warn:\n got %q\nwant %q", got.Warn, tc.wantWarn)
			}
			if got.Allowed() != (len(tc.wantDeny) == 0) {
				t.Errorf("Allowed() = %v", got.Allowed())
			}
		})
	}
}

func TestParsePolicyRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"unknown field", `{"rules":[{"name":"a","requireLabels":["team"]}]}`},
		{"no rules", `{"rules":[]}`},
		{"unnamed rule", `{"rules":[{"require":{"labels":["team"]}}]}`},
		{"no constraints", `{"rules":[{"name":"a"}]}`},
		{"duplicate names", `{"rules":[{"name":"a","require":{"labels":["x"]}},{"name":"a","require":{"labels":["y"]}}]}`},
		{"bad action", `{"rules":[{"name":"a","action":"maybe","require":{"labels":["x"]}}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parsePolicy([]byte(tc.json)); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

// A Deployment is evaluated on its pod template, so the rejection reaches the
// terminal that applied it rather than a ReplicaSet event nobody reads.
func TestDecodeObjectUsesPodTemplate(t *testing.T) {
	raw := []byte(`{
      "kind":"Deployment",
      "metadata":{"name":"web","namespace":"shop","labels":{"team":"outer"}},
      "spec":{"template":{
        "metadata":{"labels":{"team":"inner"}},
        "spec":{"containers":[{"image":"nginx:1.27"}],"initContainers":[{"image":"busybox:1.36"}]}
      }}
    }`)

	obj, err := DecodeObject(raw, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Labels["team"] != "inner" {
		t.Errorf("labels came from the wrong place: %v", obj.Labels)
	}
	if want := []string{"busybox:1.36", "nginx:1.27"}; !reflect.DeepEqual(obj.Images, want) {
		t.Errorf("images = %v, want %v", obj.Images, want)
	}
	if obj.Namespace != "shop" || obj.Kind != "Deployment" {
		t.Errorf("got %s/%s", obj.Namespace, obj.Kind)
	}
}

func TestDecodeObjectBarePod(t *testing.T) {
	raw := []byte(`{"kind":"Pod","metadata":{"generateName":"web-"},"spec":{"containers":[{"image":"nginx:1.27"}]}}`)
	obj, err := DecodeObject(raw, "default")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Name != "web-(generated)" {
		t.Errorf("name = %q", obj.Name)
	}
	if obj.Namespace != "default" {
		t.Errorf("namespace = %q, want the request's namespace", obj.Namespace)
	}
}

func TestSourceReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"rules":[{"name":"a","require":{"labels":["team"]}}]}`)

	source, err := NewSource(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go source.Watch(ctx, 20*time.Millisecond)

	if !source.Policy().Evaluate(Object{Labels: map[string]string{"team": "x"}}).Allowed() {
		t.Fatal("should allow before the edit")
	}

	write(`{"rules":[{"name":"a","require":{"labels":["owner"]}}]}`)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !source.Policy().Evaluate(Object{Labels: map[string]string{"team": "x"}}).Allowed() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("policy never reloaded")
}

func TestSourceKeepsLastGoodPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	os.WriteFile(path, []byte(`{"rules":[{"name":"a","require":{"labels":["team"]}}]}`), 0o644)

	source, err := NewSource(path)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`{ not json`), 0o644)
	if err := source.reload(); err == nil {
		t.Fatal("expected the bad file to error")
	}
	if source.Policy() == nil || len(source.Policy().Rules) != 1 {
		t.Fatal("a bad file must not drop the running policy")
	}
}
