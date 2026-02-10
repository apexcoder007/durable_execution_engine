package onboarding

import (
	"fmt"
	"os"
	"strings"
)

type Input struct {
	EmployeeID string
	Name       string
	Email      string
}

type Options struct {
	StateDir string
	Crash    CrashSpec
}

type CrashSpec struct {
	Step  string
	Point string // before | after
}

func (c CrashSpec) Enabled() bool {
	return strings.TrimSpace(c.Step) != ""
}

func (c CrashSpec) MaybeCrash(stepID, point string) {
	if !c.Enabled() {
		return
	}
	if strings.EqualFold(strings.TrimSpace(c.Step), stepID) && strings.EqualFold(strings.TrimSpace(c.Point), point) {
		fmt.Fprintf(os.Stderr, "simulating crash at %s (%s side effect)\n", stepID, point)
		os.Exit(42)
	}
}

type EmployeeRecord struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	CreatedAt  string `json:"created_at"`
}

type LaptopProvision struct {
	EmployeeID string `json:"employee_id"`
	LaptopID   string `json:"laptop_id"`
	Status     string `json:"status"`
}

type AccessProvision struct {
	EmployeeID string `json:"employee_id"`
	Role       string `json:"role"`
	Status     string `json:"status"`
}

type WelcomeEmail struct {
	EmployeeID string `json:"employee_id"`
	EmailID    string `json:"email_id"`
	SentAt     string `json:"sent_at"`
}
