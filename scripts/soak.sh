#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ITERATIONS=10

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iterations)
      ITERATIONS="$2"
      shift 2
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 1
      ;;
  esac
done

mkdir -p /tmp/go-cache /tmp/go-tmp
export GOCACHE="${GOCACHE:-/tmp/go-cache}"
export GOTMPDIR="${GOTMPDIR:-/tmp/go-tmp}"

cd "$ROOT"
for ((i=1; i<=ITERATIONS; i++)); do
  echo "=== Soak iteration $i/$ITERATIONS ==="
  go test ./engine -run 'TestRandomizedResumeProducesDeterministicOutputs|TestHighContentionManyWorkflowsParallel|TestParallelStepsAreThreadSafe' -count=1 -v
done

echo "Soak completed: $ITERATIONS iterations"

