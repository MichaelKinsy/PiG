// Builds the scripted faux responses for one probe fixture; shared by probe.mjs and extension.mjs.
// `factory: "value"` queues a response factory that returns the message; `"reject"` queues an async factory that throws.
// faux.ts:resolveResponse awaits a factory's result inside the async function, then the caller awaits resolveResponse.
export function fauxResponses(ai, fixture) {
  if (fixture.noResponse) return [];
  const message = ai.fauxAssistantMessage(fixture.content.map((block) =>
    block.type === 'text' ? ai.fauxText(block.text)
      : block.type === 'thinking' ? ai.fauxThinking(block.thinking)
      : ai.fauxToolCall(block.name, block.arguments, { id: block.id })), { timestamp: 1, ...fixture.message });
  // A tool-use turn is followed by the agent loop's next request; answer it so an RPC run settles without a retry.
  const followUp = fixture.message.stopReason === 'toolUse' ? [ai.fauxAssistantMessage('done', { timestamp: 2 })] : [];
  if (fixture.factory === 'value') return [() => message, ...followUp];
  if (fixture.factory === 'reject') return [async () => { throw new Error('scripted factory failure'); }];
  return [message, ...followUp];
}
