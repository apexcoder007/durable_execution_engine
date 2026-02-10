package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Context struct {
	WorkflowID    string
	RunID         string
	ZombieTimeout time.Duration

	store *Store

	seqMu        sync.Mutex
	stepCounters map[string]int
	claimMu      sync.Mutex
}

func NewContext(workflowID string, store *Store) *Context {
	return &Context{
		WorkflowID:    workflowID,
		RunID:         newRunID(),
		ZombieTimeout: 0,
		store:         store,
		stepCounters:  make(map[string]int),
	}
}

func (c *Context) WithZombieTimeout(d time.Duration) *Context {
	c.ZombieTimeout = d
	return c
}

type stepRef struct {
	StepID   string
	Sequence int
	StepKey  string
}

func (c *Context) nextStepRef(id string) stepRef {
	stepID := resolveStepID(id)

	c.seqMu.Lock()
	c.stepCounters[stepID]++
	seq := c.stepCounters[stepID]
	c.seqMu.Unlock()

	return stepRef{
		StepID:   stepID,
		Sequence: seq,
		StepKey:  fmt.Sprintf("%s#%06d", stepID, seq),
	}
}

func resolveStepID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = autoStepID()
	}
	id = strings.ToLower(id)

	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	clean := strings.Trim(b.String(), "_")
	if clean == "" {
		clean = "step"
	}
	return clean
}

func autoStepID() string {
	pc, file, line, ok := runtime.Caller(3)
	if !ok {
		return "auto_step_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	fn := runtime.FuncForPC(pc)
	fnName := "fn"
	if fn != nil {
		name := fn.Name()
		lastSlash := strings.LastIndex(name, "/")
		if lastSlash >= 0 {
			name = name[lastSlash+1:]
		}
		fnName = strings.ReplaceAll(name, ".", "_")
	}
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	return fmt.Sprintf("%s_%d_%s", base, line, fnName)
}

func newRunID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("run-%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf))
}
