package codingagent

import (
	"context"
	"sync"
)

type activeModelCatalogRefresh struct {
	cancel  context.CancelFunc
	done    chan struct{}
	result  CatalogRefreshResult
	waiters int
}

type modelCatalogRefreshCoordinator struct {
	mu              sync.Mutex
	activeByRuntime map[*ModelRegistry]*activeModelCatalogRefresh
}

var modelCatalogRefreshes modelCatalogRefreshCoordinator

// RefreshModelCatalogs shares concurrent interactive all-catalog refreshes for a registry while keeping each caller's cancellation independent. The last departing caller cancels the operation; a later caller can start a new refresh without waiting for abandoned work to settle.
func RefreshModelCatalogs(ctx context.Context, registry *ModelRegistry) (CatalogRefreshResult, error) {
	return modelCatalogRefreshes.refresh(ctx, registry)
}

func (c *modelCatalogRefreshCoordinator) refresh(ctx context.Context, registry *ModelRegistry) (CatalogRefreshResult, error) {
	if err := ctx.Err(); err != nil {
		return CatalogRefreshResult{}, err
	}
	if registry == nil {
		return CatalogRefreshResult{Errors: map[string]error{}}, nil
	}
	c.mu.Lock()
	active := c.activeByRuntime[registry]
	if active == nil {
		operationCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		active = &activeModelCatalogRefresh{cancel: cancel, done: make(chan struct{})}
		if c.activeByRuntime == nil {
			c.activeByRuntime = make(map[*ModelRegistry]*activeModelCatalogRefresh)
		}
		c.activeByRuntime[registry] = active
		go c.run(operationCtx, registry, active)
	}
	active.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		active.waiters--
		if active.waiters == 0 && c.activeByRuntime[registry] == active {
			delete(c.activeByRuntime, registry)
			active.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return CatalogRefreshResult{}, ctx.Err()
	case <-active.done:
		return active.result, nil
	}
}

func (c *modelCatalogRefreshCoordinator) run(ctx context.Context, registry *ModelRegistry, active *activeModelCatalogRefresh) {
	defer active.cancel()
	if registry.refreshContext(ctx).Aborted {
		active.result = CatalogRefreshResult{Aborted: true, Errors: map[string]error{}}
	} else {
		active.result = registry.RefreshCatalogs(ctx, CatalogRefreshOptions{AllowNetwork: ModelNetworkEnabled()})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeByRuntime[registry] == active {
		delete(c.activeByRuntime, registry)
	}
	close(active.done)
}
