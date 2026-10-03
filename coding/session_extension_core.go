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
	if model == nil {
		return false, errors.New("setModel requires a model")
	}
	if !s.modelRuntime.hasConfiguredAuth(ctx, providerID(model)) {
		return false, nil
	}
	if err := s.SetModel(model); err != nil {
		return false, err
	}
	return true, nil
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
