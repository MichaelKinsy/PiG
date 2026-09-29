import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { readFileSync } from 'node:fs';
const root = resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules');
const { APIError } = await import(pathToFileURL(`${root}/openai/core/error.mjs`));
const { normalizeProviderError, formatProviderError } = await import(pathToFileURL(`${root}/@earendil-works/pi-ai/dist/utils/error-body.js`));
const { status, body, prefix } = JSON.parse(readFileSync(0, 'utf8'));
let parsed; try { parsed = JSON.parse(body); } catch {}
const error = APIError.generate(status, parsed, parsed ? undefined : body, new Headers());
console.log(JSON.stringify(formatProviderError(normalizeProviderError(error), prefix)));
