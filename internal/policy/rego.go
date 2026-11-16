// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"context"
	"fmt"
	"sync"

	"github.com/open-policy-agent/opa/v1/rego"
)

// Engine caches one compiled policy and disables network access during evaluation.
type Engine struct {
	mu     sync.Mutex
	source string
	query  rego.PreparedEvalQuery
}

func (e *Engine) Allow(ctx context.Context, source string, input map[string]any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(source) == 0 || len(source) > 65536 {
		return false, fmt.Errorf("policy must contain 1..65536 bytes")
	}
	e.mu.Lock()
	if source != e.source {
		query, err := rego.New(rego.Query("data.guarded.allow"), rego.Module("policy.rego", source), rego.StrictBuiltinErrors(true),
			rego.UnsafeBuiltins(map[string]struct{}{"http.send": {}, "net.lookup_ip_addr": {}, "opa.runtime": {}})).PrepareForEval(ctx)
		if err != nil {
			e.mu.Unlock()
			return false, fmt.Errorf("prepare policy: %w", err)
		}
		e.source, e.query = source, query
	}
	query := e.query
	e.mu.Unlock()
	results, err := query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, fmt.Errorf("evaluate policy: %w", err)
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return false, nil
	}
	allowed, ok := results[0].Expressions[0].Value.(bool)
	if !ok {
		return false, fmt.Errorf("policy decision must be boolean")
	}
	return allowed, nil
}
