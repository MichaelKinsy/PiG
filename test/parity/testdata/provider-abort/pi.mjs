import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const { registerFauxProvider, complete, stream, fauxAssistantMessage } = await import(pathToFileURL(join(root, 'node_modules/@earendil-works/pi-ai/dist/compat.js')));
const registration = registerFauxProvider();
try {
 registration.setResponses([fauxAssistantMessage('ignored')]);
 const controller = new AbortController(); controller.abort();
 const context = { messages: [{ role: 'user', content: 'Hello, how are you?', timestamp: 0 }] };
 const aborted = await complete(registration.getModel(), context, { signal: controller.signal });
 context.messages.push(aborted, { role: 'user', content: 'What is 2 + 2?', timestamp: 0 });
 registration.setResponses([request => request.messages.length === 3 && request.messages[1].stopReason === 'aborted' && request.messages[1].content.length === 0 ? fauxAssistantMessage('4') : fauxAssistantMessage('', { stopReason: 'error', errorMessage: 'missing aborted history' })]);
 const follow = await complete(registration.getModel(), context);
 const paced = registerFauxProvider({ tokensPerSecond: 100, tokenSize: { min: 1, max: 1 } });
 let mid;
 try {
  paced.setResponses([fauxAssistantMessage('abcdefghijklmnopqrstuvwxyz'.repeat(4))]);
  const stop = new AbortController();
  const events = stream(paced.getModel(), { messages: [{ role: 'user', content: 'List names', timestamp: 0 }] }, { signal: stop.signal });
  let length = 0;
  for await (const event of events) { if (event.type === 'text_delta') length += event.delta.length; if (length >= 50) stop.abort(); }
  mid = await events.result();
 } finally { paced.unregister(); }
 console.log(JSON.stringify({ aborted: aborted.stopReason, content: aborted.content.length, followUp: follow.stopReason, midAborted: mid.stopReason, midContent: mid.content.length > 0, text: follow.content[0]?.text ?? '' }));
} finally { registration.unregister(); }
