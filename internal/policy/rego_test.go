// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMaintenancePolicy(t *testing.T) {
	source, err := os.ReadFile("../../policies/maintenance.rego")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name              string
		age               time.Duration
		succeeded, freeze bool
		deadline          int
		want              bool
	}{
		{"fresh", time.Hour, true, false, 600, true},
		{"exact freshness boundary", 24 * time.Hour, true, false, 600, true},
		{"stale", 24*time.Hour + time.Second, true, false, 600, false},
		{"future evidence", -time.Second, true, false, 600, false},
		{"failed backup", time.Hour, false, false, 600, false},
		{"freeze", time.Hour, true, true, 600, false},
		{"excessive runtime", time.Hour, true, false, 601, false},
	}
	engine := &Engine{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"now_unix": now.Unix(), "job": map[string]any{"activeDeadlineSeconds": tc.deadline},
				"facts": map[string]any{"backup": map[string]any{"succeeded": tc.succeeded, "completedAt": now.Add(-tc.age).Format(time.RFC3339)}, "maintenanceFreeze": tc.freeze}}
			got, err := engine.Allow(t.Context(), string(source), input)
			if err != nil || got != tc.want {
				t.Fatalf("Allow = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	allowed, err := engine.Allow(t.Context(), string(source), map[string]any{})
	if err != nil || allowed {
		t.Fatalf("missing evidence must deny: %v %v", allowed, err)
	}
}

func TestPolicyErrorsFailClosed(t *testing.T) {
	for _, source := range []string{"", "not valid rego", "package guarded\nallow := 1", "package guarded\nallow := http.send({\"method\":\"GET\",\"url\":\"https://example.org\"})"} {
		allowed, err := (&Engine{}).Allow(t.Context(), source, nil)
		if allowed || err == nil {
			t.Fatalf("invalid policy must error: %v %v", allowed, err)
		}
	}
	allowed, err := (&Engine{}).Allow(t.Context(), "package guarded\nother := true", nil)
	if err != nil || allowed {
		t.Fatalf("undefined decision must deny: %v %v", allowed, err)
	}
}

func TestConcurrentEvaluationAndCancellation(t *testing.T) {
	engine := &Engine{}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			ok, err := engine.Allow(t.Context(), "package guarded\nallow := true", nil)
			if err != nil || !ok {
				t.Errorf("concurrent evaluation: %v %v", ok, err)
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if allowed, err := engine.Allow(ctx, "package guarded\nallow := true", nil); allowed || err == nil {
		t.Fatalf("cancelled evaluation: %v %v", allowed, err)
	}
}
