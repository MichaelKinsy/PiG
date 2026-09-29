package coding

// Ports packages/coding-agent/src/core/agent-session.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// CreateReplacedSessionContext captures the current bound command context and the Session's direct awaited message methods. Runtime calls it only after host rebinding finishes.
func (s *Session) CreateReplacedSessionContext(ctx context.Context) *extension.ReplacedSessionContext {
	runner := s.currentRunner()
	if runner == nil {
		runner = inproc.NewRunner(nil, s.CWD())
		s.ReplaceRunner(runner)
		s.bindExtensionCore(runner)
	}
	command := runner.CreateCommandContext()
	ctx = extension.WithCommandContext(ctx, command)
	return extension.NewReplacedSessionContext(command,
		func(message extension.CustomMessageRef, options *extension.SendMessageOptions) error {
			return s.SendCustomMessage(ctx, message, options)
		},
		func(content any, options *extension.ReplacedSessionSendUserMessageOptions) error {
			return s.SendUserMessage(ctx, content, options)
		})
}
