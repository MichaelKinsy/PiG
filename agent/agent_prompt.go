package agent

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// Prompt is Pi's agent.prompt(input, images?) (agent.ts:372): it starts a run from a user message made of the text and the images and returns when the run settles. Pi's method resolves to nothing; the produced messages are the transcript's new tail, which Send returns to a caller that wants them.
//
// Like Pi, it fails when a run is already active.
func (a *Agent) Prompt(ctx context.Context, input string, images ...ai.ImageContent) error {
	content := make([]ai.UserContentBlock, 0, 1+len(images))
	content = append(content, ai.TextContent{Text: input})
	for _, image := range images {
		content = append(content, image)
	}
	_, err := a.SendContent(ctx, content)
	return err
}

// PromptMessages is Pi's agent.prompt(message | message[]) overload (agent.ts:371): it starts a run from the given messages.
// Like Pi's prompt([]) (agent.ts:371-379), an empty batch still claims the agent and runs a turn from the current transcript; SendMessages instead returns without a run.
func (a *Agent) PromptMessages(ctx context.Context, messages []AgentMessage) error {
	run, err := a.BeginSendMessages(ctx, messages)
	if err != nil {
		return err
	}
	_, err = run.Run()
	return err
}

// Continue is Pi's agent.continue() (agent.ts:384): it continues from the current transcript and returns nothing but the error. ContinueMessages returns the produced messages as well.
func (a *Agent) Continue(ctx context.Context) error {
	_, err := a.ContinueMessages(ctx)
	return err
}
