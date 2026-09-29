import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { readFileSync } from 'node:fs';
const input = JSON.parse(readFileSync(0, 'utf8'));
const module = await import(pathToFileURL(resolve(`extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/${input.provider}.js`)));
const provider = Object.values(module).find(value => typeof value === 'function')();
const calls = [];
const result = await provider.auth.apiKey.resolve({
  signal: new AbortController().signal,
  credential: input.credential,
  ctx: {
    env: async name => { calls.push(`env:${name}`); return input.env[name]; },
    fileExists: async path => { calls.push(`file:${path}`); return false; },
  },
});
console.log(JSON.stringify({ result: result ?? null, calls }));
