package ai

import "context"

// AuthOperationOptions is Pi's AuthOperationOptions: optional cancellation of a public auth or credential operation. A nil Signal
// is an operation that only its own context cancels; a cancelled Signal cancels the operation with the Signal's cancellation cause
// (Pi rejects with `signal.reason`).
//
// upstream: packages/ai/src/auth/types.ts:AuthOperationOptions
type AuthOperationOptions struct {
	Signal context.Context
}

// ModelsStoreOperationOptions is Pi's ModelsStoreOperationOptions: optional cancellation of a models-store operation, with the same
// meaning as AuthOperationOptions.Signal.
//
// upstream: packages/ai/src/models-store.ts:ModelsStoreOperationOptions
type ModelsStoreOperationOptions struct {
	Signal context.Context
}

// operationContext derives from ctx a context that a cancelled signal also cancels, with the signal's cause. The returned release
// function must be called when the operation settles.
func operationContext(ctx, signal context.Context) (context.Context, func()) {
	signal = OperationSignal(signal)
	if signal.Done() == nil {
		return ctx, func() {}
	}
	merged, cancel := context.WithCancelCause(ctx)
	if signal.Err() != nil {
		cancel(context.Cause(signal))
		return merged, func() {}
	}
	stop := context.AfterFunc(signal, func() { cancel(context.Cause(signal)) })
	return merged, func() {
		stop()
		cancel(context.Canceled)
	}
}

// CheckAuthWith is Models.checkAuth(providerId, options): CheckAuth cancelled also by options.Signal.
func (m *Models) CheckAuthWith(ctx context.Context, providerID string, options AuthOperationOptions) (*AuthCheck, error) {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return m.CheckAuth(ctx, providerID)
}

// GetAvailableWith is Models.getAvailable(providerId, options). An empty providerID is every provider, as Pi's omitted one
// (GetAvailable already reads an empty id so).
func (m *Models) GetAvailableWith(ctx context.Context, providerID string, options AuthOperationOptions) ([]*Model, error) {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return m.GetAvailable(ctx, providerID)
}

// GetAvailableOfTypeWith is Models.getAvailableOfType(type, providerId, options).
func (m *Models) GetAvailableOfTypeWith(ctx context.Context, modelType ModelType, providerID string, options AuthOperationOptions) ([]AnyModel, error) {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return m.GetAvailableOfType(ctx, modelType, providerID)
}

// GetAllAvailableWith is Models.getAllAvailable(providerId, options).
func (m *Models) GetAllAvailableWith(ctx context.Context, providerID string, options AuthOperationOptions) ([]AnyModel, error) {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return m.GetAllAvailable(ctx, providerID)
}

// LogoutWith is Models.logout(providerId, options).
func (m *Models) LogoutWith(ctx context.Context, providerID string, options AuthOperationOptions) error {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return m.Logout(ctx, providerID)
}

// ReadWith is InMemoryModelsStore.read(providerId, options).
func (s *InMemoryModelsStore) ReadWith(ctx context.Context, providerID string, options ModelsStoreOperationOptions) (*ModelsStoreEntry, error) {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return s.Read(ctx, providerID)
}

// WriteWith is InMemoryModelsStore.write(providerId, entry, options).
func (s *InMemoryModelsStore) WriteWith(ctx context.Context, providerID string, entry ModelsStoreEntry, options ModelsStoreOperationOptions) error {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return s.Write(ctx, providerID, entry)
}

// DeleteWith is InMemoryModelsStore.delete(providerId, options).
func (s *InMemoryModelsStore) DeleteWith(ctx context.Context, providerID string, options ModelsStoreOperationOptions) error {
	ctx, release := operationContext(ctx, options.Signal)
	defer release()
	return s.Delete(ctx, providerID)
}
