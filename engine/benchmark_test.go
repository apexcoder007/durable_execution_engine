package engine_test

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"durableexec/engine"
	"durableexec/examples/onboarding"
)

func BenchmarkStepColdWrite(b *testing.B) {
	store := mustStore(b, filepath.Join(b.TempDir(), "bench_cold.db"))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		workflowID := fmt.Sprintf("wf-cold-%d", i)
		ctx := engine.NewContext(workflowID, store)
		_, err := engine.Step(ctx, "cold_step", func() (int, error) {
			return i, nil
		})
		if err != nil {
			b.Fatalf("cold step failed at i=%d: %v", i, err)
		}
	}
}

func BenchmarkStepCachedRead(b *testing.B) {
	store := mustStore(b, filepath.Join(b.TempDir(), "bench_cached.db"))
	const workflowID = "wf-cached"

	seedCtx := engine.NewContext(workflowID, store)
	if _, err := engine.Step(seedCtx, "cached_step", func() (int, error) { return 7, nil }); err != nil {
		b.Fatalf("seed step failed: %v", err)
	}

	var executed int64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := engine.NewContext(workflowID, store)
		v, err := engine.Step(ctx, "cached_step", func() (int, error) {
			atomic.AddInt64(&executed, 1)
			return 999, nil
		})
		if err != nil {
			b.Fatalf("cached step failed at i=%d: %v", i, err)
		}
		if v != 7 {
			b.Fatalf("cached value mismatch got=%d want=7", v)
		}
	}
	b.StopTimer()
	if got := atomic.LoadInt64(&executed); got != 0 {
		b.Fatalf("cached function executed unexpectedly: %d", got)
	}
}

func BenchmarkStepParallelWrites(b *testing.B) {
	store := mustStore(b, filepath.Join(b.TempDir(), "bench_parallel.db"))
	ctx := engine.NewContext("wf-parallel-bench", store)
	var idCounter int64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := atomic.AddInt64(&idCounter, 1)
			stepID := fmt.Sprintf("parallel_%d", id)
			_, err := engine.Step(ctx, stepID, func() (int64, error) {
				return id, nil
			})
			if err != nil {
				b.Fatalf("parallel step failed for %s: %v", stepID, err)
			}
		}
	})
}

func BenchmarkOnboardingWorkflowE2E(b *testing.B) {
	store := mustStore(b, filepath.Join(b.TempDir(), "bench_onboarding.db"))
	stateDir := filepath.Join(b.TempDir(), "state")
	opts := onboarding.Options{StateDir: stateDir}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		workflowID := fmt.Sprintf("wf-onboard-%d", i)
		ctx := engine.NewContext(workflowID, store)
		err := onboarding.Run(ctx, onboarding.Input{
			EmployeeID: fmt.Sprintf("emp-%d", i),
			Name:       fmt.Sprintf("Employee %d", i),
			Email:      fmt.Sprintf("employee-%d@example.com", i),
		}, opts)
		if err != nil {
			b.Fatalf("onboarding run failed at i=%d: %v", i, err)
		}
	}
}

func mustStore(b *testing.B, path string) *engine.Store {
	b.Helper()
	store, err := engine.NewStore(path)
	if err != nil {
		b.Fatalf("new store failed: %v", err)
	}
	return store
}
