package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
)

type StepRecord struct {
	WorkflowID string
	StepKey    string
	StepID     string
	Sequence   int
	Status     string
	OutputJSON string
	ErrorText  string
	RunID      string
	StartedAt  string
	UpdatedAt  string
}

type Store struct {
	dbPath       string
	busyTimeout  time.Duration
	maxRetries   int
	retryBackoff time.Duration

	mu sync.Mutex
}

func NewStore(dbPath string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("db path is required")
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, fmt.Errorf("sqlite3 binary not found in PATH: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil && filepath.Dir(dbPath) != "." {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	s := &Store{
		dbPath:       dbPath,
		busyTimeout:  5 * time.Second,
		maxRetries:   8,
		retryBackoff: 25 * time.Millisecond,
	}
	if err := s.initSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema() error {
	schema := `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS steps (
  workflow_id TEXT NOT NULL,
  step_key TEXT NOT NULL,
  step_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  status TEXT NOT NULL,
  output_json TEXT,
  error_text TEXT,
  run_id TEXT NOT NULL,
  started_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (workflow_id, step_key)
);
CREATE INDEX IF NOT EXISTS idx_steps_workflow_status ON steps(workflow_id, status);
`
	return s.execWrite(schema)
}

func (s *Store) GetStep(workflowID, stepKey string) (StepRecord, bool, error) {
	q := fmt.Sprintf(`
SELECT workflow_id, step_key, step_id, sequence, status, output_json, error_text, run_id, started_at, updated_at
FROM steps
WHERE workflow_id=%s AND step_key=%s
LIMIT 1;`, sqlString(workflowID), sqlString(stepKey))

	rows, err := s.queryRows(q)
	if err != nil {
		return StepRecord{}, false, err
	}
	if len(rows) == 0 {
		return StepRecord{}, false, nil
	}
	return parseStepRecord(rows[0]), true, nil
}

func (s *Store) UpsertRunning(workflowID string, ref stepRef, runID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := fmt.Sprintf(`
INSERT INTO steps(workflow_id, step_key, step_id, sequence, status, output_json, error_text, run_id, started_at, updated_at)
VALUES(%s, %s, %s, %d, %s, NULL, NULL, %s, %s, %s)
ON CONFLICT(workflow_id, step_key) DO UPDATE SET
  status=%s,
  output_json=NULL,
  error_text=NULL,
  run_id=excluded.run_id,
  started_at=excluded.started_at,
  updated_at=excluded.updated_at
WHERE steps.status <> %s;`,
		sqlString(workflowID),
		sqlString(ref.StepKey),
		sqlString(ref.StepID),
		ref.Sequence,
		sqlString(statusRunning),
		sqlString(runID),
		sqlString(now),
		sqlString(now),
		sqlString(statusRunning),
		sqlString(statusCompleted),
	)
	return s.execWrite(q)
}

func (s *Store) MarkCompleted(workflowID, stepKey, runID, outputJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := fmt.Sprintf(`
UPDATE steps
SET status=%s,
    output_json=%s,
    error_text=NULL,
    run_id=%s,
    updated_at=%s
WHERE workflow_id=%s AND step_key=%s;`,
		sqlString(statusCompleted),
		sqlString(outputJSON),
		sqlString(runID),
		sqlString(now),
		sqlString(workflowID),
		sqlString(stepKey),
	)
	return s.execWrite(q)
}

func (s *Store) MarkFailed(workflowID, stepKey, runID, errText string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	q := fmt.Sprintf(`
UPDATE steps
SET status=%s,
    error_text=%s,
    run_id=%s,
    updated_at=%s
WHERE workflow_id=%s AND step_key=%s;`,
		sqlString(statusFailed),
		sqlString(errText),
		sqlString(runID),
		sqlString(now),
		sqlString(workflowID),
		sqlString(stepKey),
	)
	return s.execWrite(q)
}

func (s *Store) ListSteps(workflowID string) ([]StepRecord, error) {
	q := fmt.Sprintf(`
SELECT workflow_id, step_key, step_id, sequence, status, output_json, error_text, run_id, started_at, updated_at
FROM steps
WHERE workflow_id=%s
ORDER BY step_key;`, sqlString(workflowID))

	rows, err := s.queryRows(q)
	if err != nil {
		return nil, err
	}
	out := make([]StepRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, parseStepRecord(row))
	}
	return out, nil
}

func (s *Store) execWrite(sql string) error {
	var lastErr error
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		s.mu.Lock()
		output, err := s.runSQLite(false, sql)
		s.mu.Unlock()
		if err == nil {
			return nil
		}
		lastErr = annotateSQLiteError(err, output)
		if !isBusyError(output) || attempt == s.maxRetries {
			return lastErr
		}
		time.Sleep(s.retryBackoff * time.Duration(attempt+1))
	}
	return lastErr
}

func (s *Store) queryRows(sql string) ([]map[string]any, error) {
	s.mu.Lock()
	output, err := s.runSQLite(true, sql)
	s.mu.Unlock()
	if err != nil {
		return nil, annotateSQLiteError(err, output)
	}

	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var rows []map[string]any
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, fmt.Errorf("parse sqlite json output: %w", err)
	}
	return rows, nil
}

func (s *Store) runSQLite(jsonMode bool, sql string) ([]byte, error) {
	busyMS := strconv.Itoa(int(s.busyTimeout / time.Millisecond))
	args := []string{"-cmd", ".timeout " + busyMS}
	if jsonMode {
		args = append([]string{"-json"}, args...)
	}
	args = append(args, s.dbPath, sql)

	cmd := exec.Command("sqlite3", args...)
	return cmd.CombinedOutput()
}

func isBusyError(output []byte) bool {
	msg := strings.ToLower(string(output))
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

func annotateSQLiteError(err error, output []byte) error {
	msg := strings.TrimSpace(string(output))
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, msg)
}

func parseStepRecord(row map[string]any) StepRecord {
	return StepRecord{
		WorkflowID: asString(row["workflow_id"]),
		StepKey:    asString(row["step_key"]),
		StepID:     asString(row["step_id"]),
		Sequence:   asInt(row["sequence"]),
		Status:     asString(row["status"]),
		OutputJSON: asString(row["output_json"]),
		ErrorText:  asString(row["error_text"]),
		RunID:      asString(row["run_id"]),
		StartedAt:  asString(row["started_at"]),
		UpdatedAt:  asString(row["updated_at"]),
	}
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(x)
		return n
	default:
		return 0
	}
}

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
