package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// modelsView resolves the current target on every access so retained members follow reloads and enforce revocation.
type modelsView struct {
	resolve func() (Models, error)
}

func (view modelsView) State() chord.ReplicatedStateOf[*ModelsState] {
	return chord.StateView(func() (chord.ReplicatedStateOf[*ModelsState], error) {
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

func (view modelsView) GetThinkingLevels(ctx context.Context) ([]ai.ModelThinkingLevel, error) {
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

func (view modelsView) SelectThinking(ctx context.Context, level ai.ModelThinkingLevel) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.SelectThinking(ctx, level)
}

type remoteModels struct{ service *chord.RemoteService }

func (models remoteModels) State() chord.ReplicatedStateOf[*ModelsState] {
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

func (models remoteModels) GetThinkingLevels(ctx context.Context) ([]ai.ModelThinkingLevel, error) {
	return chord.CallResult[[]ai.ModelThinkingLevel](ctx, models.service, "getThinkingLevels")
}

func (models remoteModels) Refresh(ctx context.Context) error {
	_, err := models.service.Call(ctx, "refresh")
	return err
}

func (models remoteModels) Select(ctx context.Context, model ModelRef) error {
	_, err := models.service.Call(ctx, "select", model)
	return err
}

func (models remoteModels) SelectThinking(ctx context.Context, level ai.ModelThinkingLevel) error {
	_, err := models.service.Call(ctx, "selectThinking", level)
	return err
}
