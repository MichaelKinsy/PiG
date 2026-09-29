package coding

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// These declarations are compile-time guards for the native invocation API consumed by the port helpers. Runtime behavior is exercised by the replacement and invocation tests.
var (
	_ func(*Session, context.Context, string, ...*PromptOptions) ([]agent.AgentMessage, error)         = (*Session).Prompt
	_ func(*Session, context.Context, any, *extension.SendUserMessageOptions) error                    = (*Session).SendUserMessage
	_ func(*Session, any, *extension.SendUserMessageOptions) error                                     = (*Session).SendExtensionUserMessage
	_ func(*Session, context.Context, extension.CustomMessageRef, *extension.SendMessageOptions) error = (*Session).SendCustomMessage
	_ func(*Session, extension.CustomMessageRef, *extension.SendMessageOptions) error                  = (*Session).SendMessage
	_ func(*Runtime, func(context.Context, *Session) error)                                            = (*Runtime).SetRebindSession
	_ func(*Runtime, context.Context, *extension.NewSessionOptions) (extension.CancelledResult, error) = (*Runtime).NewSession
)
