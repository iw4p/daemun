package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"
)

// Source is the policy file, reloaded in place.
type Source struct {
	path    string
	current atomic.Pointer[Policy]
	raw     atomic.Pointer[[]byte]
}

func NewSource(path string) (*Source, error) {
	s := &Source{path: path}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Source) Policy() *Policy { return s.current.Load() }

// Watch re-reads the file on an interval. The kubelet keeps a mounted ConfigMap
// in sync by swapping a symlink, so re-reading the path picks up a kubectl apply.
// A bad file keeps the last good policy rather than dropping enforcement.
func (s *Source) Watch(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.reload(); err != nil {
			log.Printf("keeping the current policy: %v", err)
		}
	}
}

func (s *Source) reload() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if prev := s.raw.Load(); prev != nil && bytes.Equal(*prev, raw) {
		return nil
	}

	policy, err := parsePolicy(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}

	s.current.Store(policy)
	s.raw.Store(&raw)
	log.Printf("policy loaded — %s", policy.describe())
	return nil
}

func parsePolicy(raw []byte) (*Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// A misspelled field is a policy that silently enforces nothing.
	dec.DisallowUnknownFields()

	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
