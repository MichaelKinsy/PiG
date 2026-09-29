import { streamSimple } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.js';
import { normalizeContext } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/transcript.js';
for (const [provider, apiKey] of [['fireworks','test-fireworks-key'], ['anthropic','sk-ant-oat01-test'], ['custom-messages','test-custom-key']]) {
  const model = { id: 'new-reasoner', name: 'New Reasoner', api: 'anthropic-messages', provider, baseUrl: 'http://127.0.0.1:9', reasoning: true, input: ['text'], cost: { input:0, output:0, cacheRead:0, cacheWrite:0 }, contextWindow:32768, maxTokens:12345, compat:{forceAdaptiveThinking:true}, thinkingLevelMap:{max:'low'} };
  let captured;
  const result = await streamSimple(model, normalizeContext({messages:[{role:'user',content:'test',timestamp:0}]}), {apiKey, reasoning:'max', onPayload: payload => { captured = payload; throw new Error('payload captured'); }}).result();
  if (!captured || result.stopReason !== 'error' || result.errorMessage !== 'payload captured') throw new Error(JSON.stringify(result));
  console.log(JSON.stringify({provider, thinking:captured.thinking, output_config:captured.output_config, max_tokens:captured.max_tokens}));
}
