package errgroup

import "sync"

// Group is a tiny errgroup implementation with the same execution model:
// the first non-nil error is returned by Wait.
type Group struct {
	wg      sync.WaitGroup
	errOnce sync.Once
	err     error
}

func (g *Group) Go(fn func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := fn(); err != nil {
			g.errOnce.Do(func() {
				g.err = err
			})
		}
	}()
}

func (g *Group) Wait() error {
	g.wg.Wait()
	return g.err
}
