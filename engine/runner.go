package engine

import "fmt"

type WorkflowFunc func(ctx *Context) error

func RunWorkflow(store *Store, workflowID string, fn WorkflowFunc) error {
	if store == nil {
		return fmt.Errorf("nil store")
	}
	if workflowID == "" {
		return fmt.Errorf("workflow id is required")
	}
	if fn == nil {
		return fmt.Errorf("workflow function is nil")
	}

	ctx := NewContext(workflowID, store)
	return fn(ctx)
}
