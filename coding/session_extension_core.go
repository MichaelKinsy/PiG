package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// ExtensionSetModel is the setModel action Pi binds for extensions in every mode. A provider without configured credentials answers false and changes nothing; otherwise the Session switches to model as SetModel does and answers true.
//
// upstream: agent-session.ts:3343-3347 (_bindExtensionCore setModel)
func (s *Session) ExtensionSetModel(ctx context.Context, model *ai.Model) (bool, error) {
	if configured, err := s.extensionModelConfigured(model); !configured || err != nil {
		return false, err
	}
	if err := s.SetModel(model); err != nil {
		return false, err
	}
	return true, nil
}

// ExtensionSetModelOnMain is [Session.ExtensionSetModel] for an owner loop, as [Session.SetModelOnMain] is for SetModel: only the synchronous state mutation (the model, its audit entry and the thinking level it implies) runs inside dispatch, so the mode can show the model in the same owner-loop unit, and the model_select handlers are awaited on the caller, off the loop. Pi's footer reads state.model on every frame and agent-session.ts:2644-2663 sets it before awaiting the handlers, so a frame drawn while a handler is pending already shows the new model; a mode that showed it only after this call returned would draw the old one from its cache and nothing would draw again. The notifications end when ctx or the Session ends. A superseded dispatcher can reject the mutation without executing it.
//
// upstream: agent-session.ts:3343-3347 (_bindExtensionCore setModel), 2644-2663 (setModel)
func (s *Session) ExtensionSetModelOnMain(ctx context.Context, model *ai.Model, dispatch func(func() error) error) (bool, error) {
	if configured, err := s.extensionModelConfigured(model); !configured || err != nil {
		return false, err
	}
	if err := s.SetModelOnMain(ctx, model, ModelMutationOptions{}, dispatch); err != nil {
		return false, err
	}
	return true, nil
}

// extensionModelConfigured is the check of Pi's setModel action before it selects: a nil model is an error, and a provider without configured credentials is false.
func (s *Session) extensionModelConfigured(model *ai.Model) (bool, error) {
	if model == nil {
		return false, errors.New("setModel requires a model")
	}
	return s.modelRuntime.HasConfiguredAuth(providerID(model)), nil
}

// ExtensionCompact is the compact action Pi binds for extensions in every mode. It starts a manual compaction without waiting for it and reports its result to options.OnComplete or its failure to options.OnError; without callbacks the outcome is dropped, as upstream's optional calls drop it. The compaction runs as a Session task: Close cancels and joins it, and a closed Session reports the rejection to options.OnError.
//
// upstream: agent-session.ts:3369-3379 (_bindExtensionCore compact)
func (s *Session) ExtensionCompact(options *extension.CompactOptions) {
	if options == nil {
		options = &extension.CompactOptions{}
	}
	fail := func(err error) {
		if options.OnError != nil {
			options.OnError(err)
		}
	}
	ctx := s.backgroundContext()
	if err := s.startExtensionTask(func() {
		result, err := s.CompactForExtension(ctx, options.CustomInstructions)
		if err != nil {
			fail(err)
			return
		}
		if options.OnComplete != nil {
			options.OnComplete(result)
		}
	}); err != nil {
		fail(err)
	}
}
