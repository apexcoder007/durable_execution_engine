package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"durableexec/engine"
	"durableexec/examples/onboarding"
)

func main() {
	var (
		dbPath     string
		stateDir   string
		workflowID string
		empID      string
		name       string
		email      string
		crashSpec  string
	)

	flag.StringVar(&dbPath, "db", "./durable.db", "path to sqlite database")
	flag.StringVar(&stateDir, "state-dir", "./state", "directory for simulated side-effect state")
	flag.StringVar(&workflowID, "workflow-id", "employee-onboarding-001", "workflow instance id")
	flag.StringVar(&empID, "employee-id", "emp-001", "employee id")
	flag.StringVar(&name, "name", "Ada Lovelace", "employee name")
	flag.StringVar(&email, "email", "ada@example.com", "employee email")
	flag.StringVar(&crashSpec, "crash", "", "simulate crash at <step>:<before|after>, e.g. provision_laptop:after")
	flag.Parse()

	crash, err := parseCrashSpec(crashSpec)
	if err != nil {
		exitErr(err)
	}

	store, err := engine.NewStore(dbPath)
	if err != nil {
		exitErr(err)
	}

	fmt.Printf("starting workflow %q at %s\n", workflowID, time.Now().Format(time.RFC3339))
	err = engine.RunWorkflow(store, workflowID, func(ctx *engine.Context) error {
		// In this prototype we assume one active runner per workflow.
		ctx.WithZombieTimeout(0)
		return onboarding.Run(ctx, onboarding.Input{
			EmployeeID: empID,
			Name:       name,
			Email:      email,
		}, onboarding.Options{
			StateDir: stateDir,
			Crash:    crash,
		})
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow failed: %v\n", err)
		printWorkflowSteps(store, workflowID)
		os.Exit(1)
	}

	fmt.Println("workflow completed successfully")
	printWorkflowSteps(store, workflowID)
}

func parseCrashSpec(spec string) (onboarding.CrashSpec, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return onboarding.CrashSpec{}, nil
	}
	parts := strings.Split(spec, ":")
	if len(parts) != 2 {
		return onboarding.CrashSpec{}, errors.New("crash must be in format <step>:<before|after>")
	}
	step := strings.TrimSpace(parts[0])
	point := strings.ToLower(strings.TrimSpace(parts[1]))
	if step == "" {
		return onboarding.CrashSpec{}, errors.New("crash step cannot be empty")
	}
	if point != "before" && point != "after" {
		return onboarding.CrashSpec{}, errors.New("crash point must be before or after")
	}
	return onboarding.CrashSpec{Step: step, Point: point}, nil
}

func printWorkflowSteps(store *engine.Store, workflowID string) {
	steps, err := store.ListSteps(workflowID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to read workflow steps: %v\n", err)
		return
	}
	if len(steps) == 0 {
		fmt.Println("no step rows found")
		return
	}
	fmt.Println("step checkpoints:")
	for _, step := range steps {
		fmt.Printf("  - %s status=%s run=%s updated=%s\n", step.StepKey, step.Status, step.RunID, step.UpdatedAt)
	}
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
