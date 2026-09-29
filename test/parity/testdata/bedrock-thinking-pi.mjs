import { getModel } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
import { stream } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/api/bedrock-converse-stream.js';
import { normalizeContext } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/transcript.js';
import { readFileSync } from 'node:fs';
const cases = JSON.parse(readFileSync(new URL('./bedrock-thinking-cases.json', import.meta.url), 'utf8'));
for (const row of cases) {
  const model = { ...getModel('amazon-bedrock', row.base), ...row.model };
  let captured;
  const result = await stream(model, normalizeContext({ systemPrompt: 'You are helpful.', messages: [{ role: 'user', content: 'Hello', timestamp: 1 }] }), {
    reasoning: row.level, ...row.options,
    env: { AWS_BEDROCK_SKIP_AUTH: '1', AWS_REGION: '', AWS_DEFAULT_REGION: '', ...row.options?.env },
    onPayload: payload => { captured = payload; throw new Error('payload captured'); },
  }).result();
  if (!captured || result.stopReason !== 'error' || result.errorMessage !== 'payload captured') throw new Error(JSON.stringify(result));
  console.log(JSON.stringify({ name: row.name, fields: captured.additionalModelRequestFields, systemCache: captured.system.some(block => 'cachePoint' in block), messageCache: captured.messages.at(-1).content.some(block => 'cachePoint' in block) }));
}
