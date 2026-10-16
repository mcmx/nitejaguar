package common

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// ActionBuilder constructs a single action bound to the result channel it
// should emit to. The retry decorator uses it to build a fresh inner action
// per attempt on a private channel, which is what lets the framework observe
// one attempt's result without changing the Action interface.
type ActionBuilder func(out chan ResultData, args ActionArgs) (Action, error)

// retryAction wraps an action with a RetryPolicy: it re-runs the action on
// failure up to Max extra times, applying exponential backoff, and enforces
// a per-attempt Timeout. Each attempt is emitted to the engine channel so
// every try is observable; intermediate failures are marked Retrying so the
// engine persists but does not route them. The final attempt stays
// Retrying=false and routes like any ordinary result.
type retryAction struct {
	build  ActionBuilder
	events chan ResultData
	data   ActionArgs
	policy RetryPolicy

	stopOnce sync.Once
	stop     chan struct{}

	mu    sync.Mutex
	inner Action
}

// NewRetryAction binds an ActionBuilder and its args to a RetryPolicy read
// from args.Retry. The inner action is (re)built on every attempt.
func NewRetryAction(build ActionBuilder, events chan ResultData, data ActionArgs) (Action, error) {
	if build == nil {
		return nil, fmt.Errorf("retry: nil action builder")
	}
	return &retryAction{
		build:  build,
		events: events,
		data:   data,
		policy: data.Retry,
		stop:   make(chan struct{}),
	}, nil
}

// Execute runs the attempt loop. It blocks until the node succeeds, its
// retries are exhausted, its deadline fires, or Stop is called.
func (r *retryAction) Execute(executionID string, inputs []any) {
	attempts := 1
	if r.policy.Max > 0 {
		attempts = 1 + r.policy.Max
	}
	for i := 1; i <= attempts; i++ {
		res, timedOut, stopped := r.attempt(executionID, inputs)
		if stopped {
			return
		}
		res.Attempt = i
		failed := isFailure(res.Payload)
		res.Retrying = failed && !timedOut && i < attempts
		r.events <- res
		if !res.Retrying {
			return
		}
		if !r.sleep(backoffFor(r.policy, i)) {
			return
		}
	}
}

// attempt builds a fresh inner action on a private buffered channel, runs it,
// and returns its result. timedOut is true when the per-attempt deadline
// fired (terminal); stopped is true when Stop interrupted the wait. The
// private channel is buffered so a late-emitting action never blocks after
// the decorator has moved on.
func (r *retryAction) attempt(executionID string, inputs []any) (res ResultData, timedOut, stopped bool) {
	out := make(chan ResultData, 1)
	inner, err := r.build(out, r.data)
	if err != nil {
		return r.synthError(executionID, err.Error(), false), false, false
	}
	r.mu.Lock()
	r.inner = inner
	r.mu.Unlock()

	go inner.Execute(executionID, inputs)

	if r.policy.Timeout <= 0 {
		select {
		case res := <-out:
			return res, false, false
		case <-r.stop:
			_ = inner.Stop()
			return ResultData{}, false, true
		}
	}
	timer := time.NewTimer(r.policy.Timeout)
	defer timer.Stop()
	select {
	case res := <-out:
		return res, false, false
	case <-timer.C:
		_ = inner.Stop()
		return r.synthError(executionID, fmt.Sprintf("node timed out after %s", r.policy.Timeout), true), true, false
	case <-r.stop:
		_ = inner.Stop()
		return ResultData{}, false, true
	}
}

// synthError builds a decorator-generated error result (build failure or
// deadline). timeout marks the deadline case so callers can route it apart.
func (r *retryAction) synthError(executionID, msg string, timeout bool) ResultData {
	payload := map[string]any{"type": "error", "result": msg}
	if timeout {
		payload["timeout"] = true
	}
	return ResultData{
		ExecutionID: executionID,
		ActionID:    r.data.Id,
		ActionType:  r.data.ActionType,
		ActionName:  r.data.ActionName,
		Payload:     payload,
	}
}

// sleep waits d unless Stop fires first. It reports false when interrupted.
func (r *retryAction) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.stop:
		return false
	}
}

// Stop ends the attempt loop. It is idempotent and also stops the inner
// action currently running, if any.
func (r *retryAction) Stop() error {
	r.stopOnce.Do(func() { close(r.stop) })
	r.mu.Lock()
	inner := r.inner
	r.mu.Unlock()
	if inner != nil {
		_ = inner.Stop()
	}
	return nil
}

// GetArgs returns the wrapped action args (with the policy attached).
func (r *retryAction) GetArgs() ActionArgs { return r.data }

// backoffFor returns the delay before the attempt after the given 1-based
// attempt number: base doubled per retry, capped by BackoffMax. Defaults
// apply when the policy leaves them unset.
func backoffFor(p RetryPolicy, attempt int) time.Duration {
	base := p.Backoff
	if base <= 0 {
		base = time.Second
	}
	max := p.BackoffMax
	if max <= 0 {
		max = 30 * time.Second
	}
	d := base
	for i := 1; i < attempt; i++ {
		if d >= max {
			break
		}
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// isFailure reports whether a result payload signals a failure. The engine
// convention is a payload carrying type == "error"; actions emit either a
// map[string]any (most) or a struct with a `type` json field (fileaction),
// so both are recognised. Anything else counts as success.
func isFailure(payload any) bool {
	switch m := payload.(type) {
	case nil:
		return false
	case map[string]any:
		t, _ := m["type"].(string)
		return t == "error"
	case map[string]string:
		return m["type"] == "error"
	}
	v := reflect.ValueOf(payload)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag != "type" && f.Name != "Type" {
			continue
		}
		if s, ok := v.Field(i).Interface().(string); ok {
			return s == "error"
		}
	}
	return false
}
