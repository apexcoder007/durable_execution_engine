package onboarding

import (
	"fmt"
	"sync"

	"durableexec/engine"
	"durableexec/internal/errgroup"
)

func Run(ctx *engine.Context, input Input, opts Options) error {
	if input.EmployeeID == "" {
		return fmt.Errorf("employee id is required")
	}
	if input.Email == "" {
		return fmt.Errorf("employee email is required")
	}
	if input.Name == "" {
		return fmt.Errorf("employee name is required")
	}

	services, err := NewServices(opts.StateDir)
	if err != nil {
		return err
	}

	record, err := engine.Step(ctx, "create_record", func() (EmployeeRecord, error) {
		opts.Crash.MaybeCrash("create_record", "before")
		out, callErr := services.CreateRecord(input)
		opts.Crash.MaybeCrash("create_record", "after")
		return out, callErr
	})
	if err != nil {
		return err
	}

	var (
		laptop LaptopProvision
		access AccessProvision
		mu     sync.Mutex
		g      errgroup.Group
	)

	g.Go(func() error {
		res, stepErr := engine.Step(ctx, "provision_laptop", func() (LaptopProvision, error) {
			opts.Crash.MaybeCrash("provision_laptop", "before")
			out, callErr := services.ProvisionLaptop(record.EmployeeID)
			opts.Crash.MaybeCrash("provision_laptop", "after")
			return out, callErr
		})
		if stepErr != nil {
			return stepErr
		}
		mu.Lock()
		laptop = res
		mu.Unlock()
		return nil
	})

	g.Go(func() error {
		res, stepErr := engine.Step(ctx, "provision_access", func() (AccessProvision, error) {
			opts.Crash.MaybeCrash("provision_access", "before")
			out, callErr := services.ProvisionAccess(record.EmployeeID)
			opts.Crash.MaybeCrash("provision_access", "after")
			return out, callErr
		})
		if stepErr != nil {
			return stepErr
		}
		mu.Lock()
		access = res
		mu.Unlock()
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	_, err = engine.Step(ctx, "send_welcome_email", func() (WelcomeEmail, error) {
		opts.Crash.MaybeCrash("send_welcome_email", "before")
		out, callErr := services.SendWelcomeEmail(record.EmployeeID, record.Email, laptop.LaptopID, access.Role)
		opts.Crash.MaybeCrash("send_welcome_email", "after")
		return out, callErr
	})
	if err != nil {
		return err
	}

	return nil
}
