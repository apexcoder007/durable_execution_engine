#!/usr/bin/env bash
set -u

ROOT="$(cd "$(dirname "$0")" && pwd)"
QA_DIR="$ROOT/_qa"
GO_CACHE_DIR="${GOCACHE:-/tmp/go-cache}"
GO_TMP_DIR="${GOTMPDIR:-/tmp/go-tmp}"
MODE="standard"

PASS_COUNT=0
FAIL_COUNT=0
STEP_COUNT=0

green() { printf "\033[32m%s\033[0m\n" "$*"; }
red() { printf "\033[31m%s\033[0m\n" "$*"; }
yellow() { printf "\033[33m%s\033[0m\n" "$*"; }

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --rigorous)
        MODE="rigorous"
        shift
        ;;
      --standard)
        MODE="standard"
        shift
        ;;
      *)
        echo "Unknown arg: $1" >&2
        echo "Usage: ./qa.sh [--standard|--rigorous]" >&2
        exit 1
        ;;
    esac
  done
}

mark_pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  green "PASS: $*"
}

mark_fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  red "FAIL: $*"
}

run_step() {
  STEP_COUNT=$((STEP_COUNT + 1))
  local title="$1"
  shift
  echo
  echo "[$STEP_COUNT] $title"
  echo "+ $*"
  if "$@"; then
    mark_pass "$title"
    return 0
  fi
  mark_fail "$title"
  return 1
}

run_expect_fail() {
  STEP_COUNT=$((STEP_COUNT + 1))
  local title="$1"
  shift
  echo
  echo "[$STEP_COUNT] $title"
  echo "+ $*"
  if "$@"; then
    mark_fail "$title (unexpected success)"
    return 1
  fi
  mark_pass "$title (failed as expected)"
  return 0
}

run_in_root() {
  (cd "$ROOT" && "$@")
}

require_tools() {
  local missing=0
  for tool in go sqlite3; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      red "Missing required tool: $tool"
      missing=1
    fi
  done
  if [[ "$missing" -ne 0 ]]; then
    exit 1
  fi
}

print_banner() {
  echo "=============================================="
  echo "Durable Execution Engine QA Run"
  echo "ROOT: $ROOT"
  echo "QA_DIR: $QA_DIR"
  echo "MODE: $MODE"
  echo "GOCACHE: $GO_CACHE_DIR"
  echo "GOTMPDIR: $GO_TMP_DIR"
  echo "=============================================="
}

setup_env() {
  mkdir -p "$GO_CACHE_DIR" "$GO_TMP_DIR" "$QA_DIR"
  export GOCACHE="$GO_CACHE_DIR"
  export GOTMPDIR="$GO_TMP_DIR"
}

clean_qa() {
  rm -rf "$QA_DIR"
  mkdir -p "$QA_DIR"
}

verify_rows_eq() {
  local db="$1"
  local workflow="$2"
  local expected="$3"
  local got
  got="$(sqlite3 "$db" "SELECT COUNT(*) FROM steps WHERE workflow_id='$workflow';")"
  [[ "$got" == "$expected" ]]
}

verify_all_completed() {
  local db="$1"
  local workflow="$2"
  local got
  got="$(sqlite3 "$db" "SELECT COUNT(*) FROM steps WHERE workflow_id='$workflow' AND status<>'completed';")"
  [[ "$got" == "0" ]]
}

verify_json_valid() {
  local db="$1"
  local workflow="$2"
  local got
  got="$(sqlite3 "$db" "SELECT COUNT(*) FROM steps WHERE workflow_id='$workflow' AND json_valid(output_json)=0;")"
  [[ "$got" == "0" ]]
}

run_crash_matrix() {
  local db="$QA_DIR/t2.db"
  local state="$QA_DIR/t2_state"
  local steps=("create_record" "provision_laptop" "provision_access" "send_welcome_email")
  local points=("before" "after")
  mkdir -p "$state"

  for step in "${steps[@]}"; do
    for point in "${points[@]}"; do
      local wf="wf-crash-${step}-${point}"
      run_expect_fail "Crash injection: ${wf}" \
        run_in_root go run ./main -db "$db" -state-dir "$state" -workflow-id "$wf" -crash "${step}:${point}"

      run_step "Crash-resume completion: ${wf}" \
        run_in_root go run ./main -db "$db" -state-dir "$state" -workflow-id "$wf"

      run_step "Crash-resume rows=4: ${wf}" \
        verify_rows_eq "$db" "$wf" "4"

      run_step "Crash-resume all completed: ${wf}" \
        verify_all_completed "$db" "$wf"
    done
  done
}

run_fail_recover() {
  local db="$QA_DIR/t3.db"
  local state="$QA_DIR/t3_state"
  mkdir -p "$state"
  chmod 500 "$state"

  run_expect_fail "Fail phase: unwritable state dir" \
    run_in_root go run ./main -db "$db" -state-dir "$state" -workflow-id "wf-fail-recover"

  run_step "Fail phase has failed rows" \
    bash -lc "[[ \"\$(sqlite3 \"$db\" \"SELECT COUNT(*) FROM steps WHERE workflow_id='wf-fail-recover' AND status='failed';\")\" -ge 1 ]]"

  chmod 700 "$state"

  run_step "Recover phase rerun succeeds" \
    run_in_root go run ./main -db "$db" -state-dir "$state" -workflow-id "wf-fail-recover"

  run_step "Recover phase all completed" \
    verify_all_completed "$db" "wf-fail-recover"
}

run_cli_validation_edges() {
  local db="$QA_DIR/invalid.db"
  local state="$QA_DIR/invalid_state"

  run_expect_fail "Invalid crash format rejected" \
    run_in_root go run ./main -db "$db" -state-dir "$state" -workflow-id "wf-invalid" -crash "badformat"

  run_expect_fail "Invalid crash point rejected" \
    run_in_root go run ./main -db "$db" -state-dir "$state" -workflow-id "wf-invalid2" -crash "create_record:middle"
}

run_rigorous_pack() {
  run_step "Rigorous pack: fault + stress tests (10x)" \
    run_in_root go test ./engine -run 'TestRandomizedResumeProducesDeterministicOutputs|TestHighContentionManyWorkflowsParallel|TestCorruptedCachedOutputFailsFast|TestZombieTimeoutBlocksImmediateTakeover' -count=10 -v

  run_step "Rigorous soak script (5 iterations)" \
    run_in_root ./scripts/soak.sh --iterations 5
}

print_summary() {
  echo
  echo "================ QA Summary ================"
  echo "Passed: $PASS_COUNT"
  echo "Failed: $FAIL_COUNT"
  echo "Total : $((PASS_COUNT + FAIL_COUNT))"
  echo "Artifacts:"
  echo "  $QA_DIR/t1.db"
  echo "  $QA_DIR/t2.db"
  echo "  $QA_DIR/t3.db"
  echo "============================================"

  if [[ "$FAIL_COUNT" -ne 0 ]]; then
    exit 1
  fi
}

main() {
  parse_args "$@"
  require_tools
  print_banner
  setup_env

  run_step "Clean QA artifacts" clean_qa

  run_step "Go tests (all packages)" run_in_root go test ./... -v
  run_step "Go race tests (engine)" run_in_root go test -race ./engine -v

  run_step "E2E baseline workflow run" \
    run_in_root go run ./main \
      -db "$QA_DIR/t1.db" \
      -state-dir "$QA_DIR/t1_state" \
      -workflow-id "wf-basic" \
      -employee-id "emp-001" \
      -name "Ada Lovelace" \
      -email "ada@example.com"

  run_step "Schema contains steps table" \
    bash -lc "sqlite3 \"$QA_DIR/t1.db\" \".schema steps\" | grep -q 'CREATE TABLE steps'"

  run_step "Baseline row count is 4" verify_rows_eq "$QA_DIR/t1.db" "wf-basic" "4"
  run_step "Baseline all completed" verify_all_completed "$QA_DIR/t1.db" "wf-basic"
  run_step "Baseline JSON output valid" verify_json_valid "$QA_DIR/t1.db" "wf-basic"

  run_step "Memoization rerun with same workflow id" \
    run_in_root go run ./main -db "$QA_DIR/t1.db" -state-dir "$QA_DIR/t1_state" -workflow-id "wf-basic"

  run_step "Memoization row count still 4" verify_rows_eq "$QA_DIR/t1.db" "wf-basic" "4"
  run_step "Memoization still all completed" verify_all_completed "$QA_DIR/t1.db" "wf-basic"

  run_step "New workflow id executes independently" \
    run_in_root go run ./main \
      -db "$QA_DIR/t1.db" \
      -state-dir "$QA_DIR/t1_state" \
      -workflow-id "wf-basic-2" \
      -employee-id "emp-002" \
      -name "Grace Hopper" \
      -email "grace@example.com"

  run_step "Second workflow also has 4 rows" verify_rows_eq "$QA_DIR/t1.db" "wf-basic-2" "4"
  run_step "Second workflow all completed" verify_all_completed "$QA_DIR/t1.db" "wf-basic-2"

  run_step "State folder JSON files exist" \
    bash -lc "test -f \"$QA_DIR/t1_state/employees.json\" && test -f \"$QA_DIR/t1_state/laptops.json\" && test -f \"$QA_DIR/t1_state/access.json\" && test -f \"$QA_DIR/t1_state/emails.json\""

  run_crash_matrix
  run_fail_recover
  run_cli_validation_edges

  run_step "Repeat memoization test 20x" run_in_root go test ./engine -run TestStepMemoizationSkipsCompleted -count=20 -v
  run_step "Repeat loop sequence test 20x" run_in_root go test ./engine -run TestLoopSequenceIsStableAcrossRuns -count=20 -v
  run_step "Repeat parallel safety test 20x" run_in_root go test ./engine -run TestParallelStepsAreThreadSafe -count=20 -v
  run_step "Repeat zombie takeover test 20x" run_in_root go test ./engine -run TestZombieRunningStepIsTakenOverOnResume -count=20 -v
  run_step "Repeat auto-id test 20x" run_in_root go test ./engine -run TestAutomaticStepIDGeneration -count=20 -v

  if [[ "$MODE" == "rigorous" ]]; then
    run_rigorous_pack
  fi

  yellow "Snapshot: wf-basic rows"
  sqlite3 -box "$QA_DIR/t1.db" "SELECT workflow_id,step_key,status,run_id FROM steps WHERE workflow_id='wf-basic' ORDER BY step_key;"

  yellow "Snapshot: one crash workflow rows"
  sqlite3 -box "$QA_DIR/t2.db" "SELECT workflow_id,step_key,status,run_id FROM steps WHERE workflow_id='wf-crash-send_welcome_email-after' ORDER BY step_key;"

  print_summary
}

main "$@"
