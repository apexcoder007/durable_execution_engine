# Native Durable Execution Engine (Go)

This repository contains a working Go prototype of a native durable workflow engine that checkpoints step results in SQLite and resumes without re-running completed side effects.

## What is implemented

- Generic durable step primitive:
  - `func Step[T any](ctx *engine.Context, id string, fn func() (T, error)) (T, error)`
- Workflow runner:
  - `engine.RunWorkflow(...)`
- SQLite-backed persistence (`steps` table) with:
  - `workflow_id`
  - `step_key` (step id + logical sequence)
  - `status`
  - serialized `output_json`
- Resume behavior:
  - Completed steps return cached output and are skipped.
- Parallel step execution in onboarding example.
- Crash simulation and restart proof via CLI.
- Tests for durability, sequence behavior, concurrency safety, zombie-step takeover, and auto-ID generation.

## Project layout

- `engine/` core durable engine (context, step logic, sqlite persistence, runner)
- `examples/onboarding/` employee onboarding workflow example
- `main/` CLI app to start, crash, and resume workflow
- `internal/errgroup/` small local errgroup implementation used for parallel steps
- `scripts/soak.sh` repeated stress runner for rigorous testing
- `qa.sh` end-to-end QA runner with standard and rigorous modes
- `Prompts.txt` prompts used during AI-assisted build

## Requirements

- Go `1.25+`
- `sqlite3` binary available in `PATH`

## Run the prototype

```bash
go run ./main -workflow-id emp-onboard-001
```

### Simulate a crash

Crash format is `-crash <step>:<before|after>`.

```bash
go run ./main -workflow-id emp-onboard-001 -crash provision_laptop:after
```

Then rerun the same workflow id:

```bash
go run ./main -workflow-id emp-onboard-001
```

You should see previously completed steps reported as `completed` and skipped.

## Onboarding workflow steps

1. `create_record` (sequential)
2. `provision_laptop` (parallel)
3. `provision_access` (parallel)
4. `send_welcome_email` (sequential)

## Sequence tracking (loops/conditionals)

Each `Context` maintains a logical per-step counter:

- First call to `loop_step` -> `loop_step#000001`
- Second call to `loop_step` -> `loop_step#000002`
- etc.

This means loops and repeated branches can reuse the same human-readable step ID while still getting unique checkpoint keys.

The engine also supports automatic step ID generation if `id == ""` (bonus requirement), using caller metadata.

## Thread safety and SQLite concurrency

Parallel workflow steps are supported. For SQLite safety:

- SQLite is configured with `WAL` mode and `busy_timeout`.
- Store operations are synchronized with a mutex (single writer section).
- Write operations include retries for `SQLITE_BUSY`/`database is locked`.

This satisfies the assignment requirement of safe concurrent step execution against SQLite.

## Zombie-step handling

A zombie step is a row left in `running` state after a crash.

Current strategy in this prototype:

- On resume, if a `running` step belongs to a different `run_id`, the new run takes over and re-executes it.
- Side effects in the onboarding sample are implemented idempotently in file-backed mock services.

This keeps the prototype resilient and practical without introducing a separate distributed lease service.

## Run tests

```bash
go test ./...
```

## Rigorous test pack

Additional rigorous tests are implemented in:

- `engine/rigorous_test.go`
  - `TestRandomizedResumeProducesDeterministicOutputs`
  - `TestHighContentionManyWorkflowsParallel`
  - `TestCorruptedCachedOutputFailsFast`
  - `TestZombieTimeoutBlocksImmediateTakeover`

These add randomized resume-equivalence checks, high-contention concurrency stress, corrupted-checkpoint handling, and strict zombie-timeout behavior.

## QA script

Run the full QA suite:

```bash
./qa.sh
```

Run the rigorous mode (includes extra stress pack and soak loop):

```bash
./qa.sh --rigorous
```

## Soak runner

Run repeated stress cycles directly:

```bash
./scripts/soak.sh --iterations 10
```

## Performance benchmarks

Benchmark suite is implemented in:

- `engine/benchmark_test.go`
  - `BenchmarkStepColdWrite`
  - `BenchmarkStepCachedRead`
  - `BenchmarkStepParallelWrites`
  - `BenchmarkOnboardingWorkflowE2E`

Run all benchmarks:

```bash
mkdir -p /tmp/go-cache /tmp/go-tmp
GOCACHE=/tmp/go-cache GOTMPDIR=/tmp/go-tmp go test ./engine -run '^$' -bench . -benchmem -count 1
```

Run benchmark with CPU/memory profile:

```bash
GOCACHE=/tmp/go-cache GOTMPDIR=/tmp/go-tmp go test ./engine -run '^$' -bench BenchmarkStepParallelWrites -benchmem -cpuprofile /tmp/cpu.out -memprofile /tmp/mem.out
go tool pprof /tmp/cpu.out
go tool pprof /tmp/mem.out
```
