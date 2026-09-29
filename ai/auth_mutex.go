package ai

// Ports packages/coding-agent/src/core/auth-storage.ts.

import (
	"context"
	"sync"
)

// authMutex retains process-local snapshot ownership while allowing queued file operations to cancel before acquiring it. The active operation keeps ownership until its callback settles.
type authMutex struct {
	once  sync.Once
	token chan struct{}
}

func (m *authMutex) init() {
	m.once.Do(func() {
		m.token = make(chan struct{}, 1)
		m.token <- struct{}{}
	})
}

func (m *authMutex) Lock() {
	m.init()
	<-m.token
}

func (m *authMutex) LockContext(ctx context.Context) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	m.init()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-m.token:
		if err := context.Cause(ctx); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}

func (m *authMutex) Unlock() {
	m.token <- struct{}{}
}
