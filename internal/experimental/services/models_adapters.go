package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// modelsView resolves the current target on every access so retained members follow reloads and enforce revocation.
type modelsView struct {
	resolve func() (Models, error)
}

func (view modelsView) State() pico3.ReplicatedStateOf[*ModelsState] {
	return chord.StateView(func() (pico3.ReplicatedStateOf[*ModelsState], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

func (view modelsView) CycleThinking(ctx context.Context) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.CycleThinking(ctx)
}

func (view modelsView) GetThinkingLevels(ctx context.Context) ([]ai.ThinkingLevel, error) {
	target, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return target.GetThinkingLevels(ctx)
}

func (view modelsView) Refresh(ctx context.Context) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.Refresh(ctx)
}

func (view modelsView) Select(ctx context.Context, model ModelRef) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.Select(ctx, model)
}

func (view modelsView) SelectThinking(ctx context.Context, level ai.ThinkingLevel) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.SelectThinking(ctx, level)
}

type remoteModels struct{ service *chord.RemoteService }

func (models remoteModels) State() pico3.ReplicatedStateOf[*ModelsState] {
	replica, err := models.service.State("state")
	if err != nil {
		panic(err)
	}
	return chord.TypedReplica[*ModelsState](replica)
}

func (models remoteModels) CycleThinking(ctx context.Context) error {
	_, err := models.service.Call(ctx, "cycleThinking")
	return err
}

func (models remoteModels) GetThinkingLevels(ctx context.Context) ([]ai.ThinkingLevel, error) {
	return chord.CallResult[[]ai.ThinkingLevel](ctx, models.service, "getThinkingLevels")
}

func (models remoteModels) Refresh(ctx context.Context) error {
	_, err := models.service.Call(ctx, "refresh")
	return err
}

func (models remoteModels) Select(ctx context.Context, model ModelRef) error {
	_, err := models.service.Call(ctx, "select", model)
	return err
}

func (models remoteModels) SelectThinking(ctx context.Context, level ai.ThinkingLevel) error {
	_, err := models.service.Call(ctx, "selectThinking", level)
	return err
}
