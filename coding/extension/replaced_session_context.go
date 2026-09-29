package extension

// Ports packages/coding-agent/src/core/extensions/types.ts.

// ReplacedSessionSendUserMessageOptions controls the awaited user-message operation on a replacement context. Template and command expansion default to false.
type ReplacedSessionSendUserMessageOptions = SendUserMessageOptions

// ReplacedSessionContext is the fresh command-capable context supplied after Session replacement and host rebinding. Its message methods await the Session operation rather than launching extension-owned background work.
type ReplacedSessionContext struct {
	*CommandContext
	sendMessage     func(CustomMessageRef, *SendMessageOptions) error
	sendUserMessage func(any, *ReplacedSessionSendUserMessageOptions) error
}

// NewReplacedSessionContext binds the replacement Session's direct, awaited message operations to its command context.
func NewReplacedSessionContext(command *CommandContext, sendMessage func(CustomMessageRef, *SendMessageOptions) error, sendUserMessage func(any, *ReplacedSessionSendUserMessageOptions) error) *ReplacedSessionContext {
	return &ReplacedSessionContext{CommandContext: command, sendMessage: sendMessage, sendUserMessage: sendUserMessage}
}

// SendMessage awaits custom-message persistence and any triggered turn.
func (c *ReplacedSessionContext) SendMessage(message CustomMessageRef, options *SendMessageOptions) error {
	return c.sendMessage(message, options)
}

// SendUserMessage awaits input handling and the selected prompt or queue operation.
func (c *ReplacedSessionContext) SendUserMessage(content any, options *ReplacedSessionSendUserMessageOptions) error {
	return c.sendUserMessage(content, options)
}
