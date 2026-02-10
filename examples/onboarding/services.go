package onboarding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Services struct {
	stateDir string
	mu       sync.Mutex
}

func NewServices(stateDir string) (*Services, error) {
	if stateDir == "" {
		stateDir = "state"
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	return &Services{stateDir: stateDir}, nil
}

func (s *Services) CreateRecord(in Input) (EmployeeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.stateDir, "employees.json")
	records := make(map[string]EmployeeRecord)
	if err := readJSON(path, &records); err != nil {
		return EmployeeRecord{}, err
	}
	if existing, ok := records[in.EmployeeID]; ok {
		return existing, nil
	}

	record := EmployeeRecord{
		EmployeeID: in.EmployeeID,
		Name:       in.Name,
		Email:      in.Email,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	records[in.EmployeeID] = record
	if err := writeJSON(path, records); err != nil {
		return EmployeeRecord{}, err
	}
	return record, nil
}

func (s *Services) ProvisionLaptop(employeeID string) (LaptopProvision, error) {
	// Simulate an external service call.
	time.Sleep(250 * time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.stateDir, "laptops.json")
	records := make(map[string]LaptopProvision)
	if err := readJSON(path, &records); err != nil {
		return LaptopProvision{}, err
	}
	if existing, ok := records[employeeID]; ok {
		return existing, nil
	}

	provision := LaptopProvision{
		EmployeeID: employeeID,
		LaptopID:   "LAP-" + employeeID,
		Status:     "provisioned",
	}
	records[employeeID] = provision
	if err := writeJSON(path, records); err != nil {
		return LaptopProvision{}, err
	}
	return provision, nil
}

func (s *Services) ProvisionAccess(employeeID string) (AccessProvision, error) {
	// Simulate an external service call.
	time.Sleep(250 * time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.stateDir, "access.json")
	records := make(map[string]AccessProvision)
	if err := readJSON(path, &records); err != nil {
		return AccessProvision{}, err
	}
	if existing, ok := records[employeeID]; ok {
		return existing, nil
	}

	provision := AccessProvision{
		EmployeeID: employeeID,
		Role:       "employee",
		Status:     "granted",
	}
	records[employeeID] = provision
	if err := writeJSON(path, records); err != nil {
		return AccessProvision{}, err
	}
	return provision, nil
}

func (s *Services) SendWelcomeEmail(employeeID, email, laptopID, role string) (WelcomeEmail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.stateDir, "emails.json")
	records := make(map[string]WelcomeEmail)
	if err := readJSON(path, &records); err != nil {
		return WelcomeEmail{}, err
	}
	if existing, ok := records[employeeID]; ok {
		return existing, nil
	}

	_ = laptopID
	_ = role

	sent := WelcomeEmail{
		EmployeeID: employeeID,
		EmailID:    "WELCOME-" + employeeID,
		SentAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	records[employeeID] = sent
	if err := writeJSON(path, records); err != nil {
		return WelcomeEmail{}, err
	}
	return sent, nil
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
