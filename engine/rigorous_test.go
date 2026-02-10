package engine

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"durableexec/internal/errgroup"
)

var errIntentionalStop = errors.New("intentional stop")

func TestRandomizedResumeProducesDeterministicOutputs(t *testing.T) {
	const scenarios = 20
	for seed := 1; seed <= scenarios; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(int64(seed)))
			ops := makeRandomOps(r, 24, []string{"alpha", "beta", "gamma", "delta", "epsilon"})
			crashAfter := r.Intn(len(ops))

			storeResume := newTestStore(t)
			workflowID := fmt.Sprintf("wf-random-resume-%d", seed)

			// First attempt stops midway to simulate interruption.
			ctx1 := NewContext(workflowID, storeResume)
			err := runOpsWorkflow(ctx1, ops, crashAfter)
			if !errors.Is(err, errIntentionalStop) {
				t.Fatalf("expected intentional stop, got: %v", err)
			}

			// Resume must complete and preserve deterministic outputs.
			ctx2 := NewContext(workflowID, storeResume)
			if err := runOpsWorkflow(ctx2, ops, -1); err != nil {
				t.Fatalf("resume run failed: %v", err)
			}
			resumeRows, err := storeResume.ListSteps(workflowID)
			if err != nil {
				t.Fatalf("list resumed rows failed: %v", err)
			}

			storeClean := newTestStore(t)
			cleanWorkflowID := fmt.Sprintf("wf-random-clean-%d", seed)
			ctxClean := NewContext(cleanWorkflowID, storeClean)
			if err := runOpsWorkflow(ctxClean, ops, -1); err != nil {
				t.Fatalf("clean run failed: %v", err)
			}
			cleanRows, err := storeClean.ListSteps(cleanWorkflowID)
			if err != nil {
				t.Fatalf("list clean rows failed: %v", err)
			}

			if len(resumeRows) != len(cleanRows) {
				t.Fatalf("row count mismatch resumed=%d clean=%d", len(resumeRows), len(cleanRows))
			}

			for i := range resumeRows {
				a := resumeRows[i]
				b := cleanRows[i]
				if a.StepKey != b.StepKey {
					t.Fatalf("step key mismatch at %d: resumed=%s clean=%s", i, a.StepKey, b.StepKey)
				}
				if a.StepID != b.StepID || a.Sequence != b.Sequence {
					t.Fatalf("identity mismatch at %d: resumed=%s/%d clean=%s/%d", i, a.StepID, a.Sequence, b.StepID, b.Sequence)
				}
				if a.Status != statusCompleted || b.Status != statusCompleted {
					t.Fatalf("expected completed status at %d: resumed=%s clean=%s", i, a.Status, b.Status)
				}
				if a.OutputJSON != b.OutputJSON {
					t.Fatalf("output mismatch at %d step=%s resumed=%s clean=%s", i, a.StepKey, a.OutputJSON, b.OutputJSON)
				}
			}
		})
	}
}

func TestHighContentionManyWorkflowsParallel(t *testing.T) {
	store := newTestStore(t)
	const (
		workflowCount = 20
		stepsPerWF    = 18
	)

	var g errgroup.Group
	for w := 0; w < workflowCount; w++ {
		w := w
		g.Go(func() error {
			workflowID := fmt.Sprintf("wf-contention-%02d", w)
			ctx := NewContext(workflowID, store)

			// Phase 1: unique parallel steps.
			var parallel errgroup.Group
			for i := 0; i < 6; i++ {
				i := i
				parallel.Go(func() error {
					_, err := Step(ctx, fmt.Sprintf("parallel_unique_%02d", i), func() (string, error) {
						return fmt.Sprintf("wf=%02d:i=%02d", w, i), nil
					})
					return err
				})
			}
			if err := parallel.Wait(); err != nil {
				return err
			}

			// Phase 2: repeated IDs to stress sequence tracking.
			for i := 0; i < stepsPerWF-6; i++ {
				id := []string{"loop_a", "loop_b", "loop_c"}[i%3]
				if _, err := Step(ctx, id, func() (int, error) {
					return w*1000 + i, nil
				}); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("parallel workflows failed: %v", err)
	}

	for w := 0; w < workflowCount; w++ {
		wf := fmt.Sprintf("wf-contention-%02d", w)
		rows, err := store.ListSteps(wf)
		if err != nil {
			t.Fatalf("list steps for %s failed: %v", wf, err)
		}
		if len(rows) != stepsPerWF {
			t.Fatalf("%s expected %d rows got %d", wf, stepsPerWF, len(rows))
		}
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			if row.Status != statusCompleted {
				t.Fatalf("%s has non-completed row %s status=%s", wf, row.StepKey, row.Status)
			}
			if _, ok := seen[row.StepKey]; ok {
				t.Fatalf("%s has duplicate step_key %s", wf, row.StepKey)
			}
			seen[row.StepKey] = struct{}{}
		}
	}
}

func TestCorruptedCachedOutputFailsFast(t *testing.T) {
	store := newTestStore(t)
	workflowID := "wf-corrupt-cache"

	ctx1 := NewContext(workflowID, store)
	if _, err := Step(ctx1, "create_record", func() (int, error) { return 42, nil }); err != nil {
		t.Fatalf("seed step failed: %v", err)
	}

	if err := store.execWrite(`
UPDATE steps
SET output_json='not-json'
WHERE workflow_id='wf-corrupt-cache' AND step_key='create_record#000001';`); err != nil {
		t.Fatalf("failed to corrupt row: %v", err)
	}

	ctx2 := NewContext(workflowID, store)
	_, err := Step(ctx2, "create_record", func() (int, error) {
		return 999, nil
	})
	if err == nil {
		t.Fatalf("expected decode error from corrupted cache")
	}
	if !strings.Contains(err.Error(), "decode cached step result") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestZombieTimeoutBlocksImmediateTakeover(t *testing.T) {
	store := newTestStore(t)
	const workflowID = "wf-zombie-timeout"

	oldCtx := NewContext(workflowID, store)
	ref := oldCtx.nextStepRef("provision_access")
	if err := store.UpsertRunning(workflowID, ref, oldCtx.RunID); err != nil {
		t.Fatalf("seed running row failed: %v", err)
	}

	newCtx := NewContext(workflowID, store).WithZombieTimeout(24 * time.Hour)
	_, err := Step(newCtx, "provision_access", func() (string, error) {
		return "unexpected", nil
	})
	if err == nil {
		t.Fatalf("expected timeout-based takeover rejection")
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func runOpsWorkflow(ctx *Context, ops []string, stopAfter int) error {
	for i, id := range ops {
		idx := i
		_, err := Step(ctx, id, func() (int, error) {
			return deterministicOutput(idx, id), nil
		})
		if err != nil {
			return err
		}
		if stopAfter >= 0 && i == stopAfter {
			return errIntentionalStop
		}
	}
	return nil
}

func makeRandomOps(r *rand.Rand, n int, idPool []string) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		pick := idPool[r.Intn(len(idPool))]
		out = append(out, pick)
	}
	return out
}

func deterministicOutput(idx int, id string) int {
	h := 0
	for _, c := range id {
		h += int(c)
	}
	return idx*1000 + h
}
