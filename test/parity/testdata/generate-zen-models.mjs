// Regenerate the exact dynamic denominator of the pinned Pi zen.test.ts (MODELS of the installed pi-ai).
import { MODELS } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/models.generated.js';
console.log(JSON.stringify(['opencode', 'opencode-go'].flatMap(provider => Object.values(MODELS[provider]).map(model => ({provider, id: model.id}))), null, 2));
