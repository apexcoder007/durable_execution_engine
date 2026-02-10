package engine

import (
	"fmt"
	"testing"

	"durableexec/internal/errgroup"
)

func TestStepMemoizationSkipsCompleted(t *testing.T) {
	store := newTestStore(t)
	const workflowID = "wf-memo"

	calls := 0
	runOnce := func() (int, error) {
		ctx := NewContext(workflowID, store)
		return Step(ctx, "create_record", func() (int, error) {
			calls++
			return 7, nil
		})
	}

	v1, err := runOnce()
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	if v1 != 7 {
		t.Fatalf("unexpected first result: %d", v1)
	}

	v2, err := runOnce()
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if v2 != 7 {
		t.Fatalf("unexpected second result: %d", v2)
	}
	if calls != 1 {
		t.Fatalf("expected fn to run once, ran %d times", calls)
	}
}

func TestLoopSequenceIsStableAcrossRuns(t *testing.T) {
	store := newTestStore(t)
	const workflowID = "wf-loop"

	ctx1 := NewContext(workflowID, store)
	for i := 0; i < 3; i++ {
		want := i
		got, err := Step(ctx1, "loop_step", func() (int, error) {
			return want, nil
		})
		if err != nil {
			t.Fatalf("first run loop step %d failed: %v", i, err)
		}
		if got != want {
			t.Fatalf("first run loop step %d got=%d want=%d", i, got, want)
		}
	}

	rerunCalls := 0
	ctx2 := NewContext(workflowID, store)
	for i := 0; i < 3; i++ {
		want := i
		got, err := Step(ctx2, "loop_step", func() (int, error) {
			rerunCalls++
			return 999, nil
		})
		if err != nil {
			t.Fatalf("second run loop step %d failed: %v", i, err)
		}
		if got != want {
			t.Fatalf("second run loop step %d got=%d want cached=%d", i, got, want)
		}
	}
	if rerunCalls != 0 {
		t.Fatalf("expected cached loop steps on rerun, but fn ran %d times", rerunCalls)
	}
}

func TestParallelStepsAreThreadSafe(t *testing.T) {
	store := newTestStore(t)
	const workflowID = "wf-parallel"

	ctx := NewContext(workflowID, store)
	var g errgroup.Group
	for i := 0; i < 24; i++ {
		i := i
		g.Go(func() error {
			_, err := Step(ctx, fmt.Sprintf("parallel_%02d", i), func() (string, error) {
				return fmt.Sprintf("ok-%02d", i), nil
			})
			return err
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("parallel run failed: %v", err)
	}

	rows, err := store.ListSteps(workflowID)
	if err != nil {
		t.Fatalf("list steps failed: %v", err)
	}
	if len(rows) != 24 {
		t.Fatalf("expected 24 rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.Status != statusCompleted {
			t.Fatalf("step %s has unexpected status %s", row.StepKey, row.Status)
		}
	}
}

func TestZombieRunningStepIsTakenOverOnResume(t *testing.T) {
	store := newTestStore(t)
	const workflowID = "wf-zombie"

	oldCtx := NewContext(workflowID, store)
	ref := oldCtx.nextStepRef("provision_access")
	if err := store.UpsertRunning(workflowID, ref, oldCtx.RunID); err != nil {
		t.Fatalf("seed running row failed: %v", err)
	}

	newCtx := NewContext(workflowID, store).WithZombieTimeout(0)
	calls := 0
	out, err := Step(newCtx, "provision_access", func() (string, error) {
		calls++
		return "done", nil
	})
	if err != nil {
		t.Fatalf("resume step failed: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected output: %s", out)
	}
	if calls != 1 {
		t.Fatalf("expected step function to run once, ran %d times", calls)
	}

	row, found, err := store.GetStep(workflowID, ref.StepKey)
	if err != nil {
		t.Fatalf("load row failed: %v", err)
	}
	if !found {
		t.Fatalf("expected row %s to exist", ref.StepKey)
	}
	if row.Status != statusCompleted {
		t.Fatalf("expected completed status, got %s", row.Status)
	}
	if row.RunID == oldCtx.RunID {
		t.Fatalf("expected run ownership to change from old run")
	}
}

func TestAutomaticStepIDGeneration(t *testing.T) {
	store := newTestStore(t)
	const workflowID = "wf-auto"

	calls := 0
	invoke := func(ctx *Context) error {
		_, err := Step(ctx, "", func() (string, error) {
			calls++
			return "auto", nil
		})
		return err
	}

	if err := invoke(NewContext(workflowID, store)); err != nil {
		t.Fatalf("first invoke failed: %v", err)
	}
	if err := invoke(NewContext(workflowID, store)); err != nil {
		t.Fatalf("second invoke failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected generated id to be stable and memoized, got calls=%d", calls)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	return store
}
