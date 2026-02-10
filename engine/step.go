package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type claimResult int

const (
	claimExecute claimResult = iota
	claimCached
)

func Step[T any](ctx *Context, id string, fn func() (T, error)) (T, error) {
	var zero T

	if ctx == nil {
		return zero, errors.New("nil durable context")
	}
	if ctx.store == nil {
		return zero, errors.New("nil durable store")
	}
	if fn == nil {
		return zero, errors.New("step function is nil")
	}

	ref := ctx.nextStepRef(id)
	claim, cachedJSON, err := ctx.claimStep(ref)
	if err != nil {
		return zero, err
	}

	if claim == claimCached {
		var out T
		if err := json.Unmarshal([]byte(cachedJSON), &out); err != nil {
			return zero, fmt.Errorf("decode cached step result for %s: %w", ref.StepKey, err)
		}
		return out, nil
	}

	result, err := fn()
	if err != nil {
		_ = ctx.store.MarkFailed(ctx.WorkflowID, ref.StepKey, ctx.RunID, err.Error())
		return zero, fmt.Errorf("step %s failed: %w", ref.StepKey, err)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		_ = ctx.store.MarkFailed(ctx.WorkflowID, ref.StepKey, ctx.RunID, "marshal error: "+err.Error())
		return zero, fmt.Errorf("marshal step result for %s: %w", ref.StepKey, err)
	}

	if err := ctx.store.MarkCompleted(ctx.WorkflowID, ref.StepKey, ctx.RunID, string(payload)); err != nil {
		return zero, fmt.Errorf("step %s executed but completion checkpoint failed (possible zombie step): %w", ref.StepKey, err)
	}
	return result, nil
}

func (c *Context) claimStep(ref stepRef) (claimResult, string, error) {
	c.claimMu.Lock()
	defer c.claimMu.Unlock()

	record, found, err := c.store.GetStep(c.WorkflowID, ref.StepKey)
	if err != nil {
		return claimExecute, "", fmt.Errorf("load step state for %s: %w", ref.StepKey, err)
	}

	if !found {
		if err := c.store.UpsertRunning(c.WorkflowID, ref, c.RunID); err != nil {
			return claimExecute, "", fmt.Errorf("insert running step %s: %w", ref.StepKey, err)
		}
		return claimExecute, "", nil
	}

	switch record.Status {
	case statusCompleted:
		return claimCached, record.OutputJSON, nil
	case statusFailed:
		if err := c.store.UpsertRunning(c.WorkflowID, ref, c.RunID); err != nil {
			return claimExecute, "", fmt.Errorf("retry failed step %s: %w", ref.StepKey, err)
		}
		return claimExecute, "", nil
	case statusRunning:
		if record.RunID == c.RunID {
			return claimExecute, "", fmt.Errorf("step %s is already running in this execution", ref.StepKey)
		}
		if !c.canTakeOverZombie(record) {
			return claimExecute, "", fmt.Errorf("step %s is still running under run_id=%s", ref.StepKey, record.RunID)
		}
		if err := c.store.UpsertRunning(c.WorkflowID, ref, c.RunID); err != nil {
			return claimExecute, "", fmt.Errorf("take over zombie step %s: %w", ref.StepKey, err)
		}
		return claimExecute, "", nil
	default:
		if err := c.store.UpsertRunning(c.WorkflowID, ref, c.RunID); err != nil {
			return claimExecute, "", fmt.Errorf("reset unknown state for step %s: %w", ref.StepKey, err)
		}
		return claimExecute, "", nil
	}
}

func (c *Context) canTakeOverZombie(record StepRecord) bool {
	if c.ZombieTimeout <= 0 {
		return true
	}
	updated, err := time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil {
		return true
	}
	return time.Since(updated) >= c.ZombieTimeout
}
