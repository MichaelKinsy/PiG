import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { readFileSync } from 'node:fs';
const { provider, answers } = JSON.parse(readFileSync(0, 'utf8'));
const module = await import(pathToFileURL(resolve(`extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/${provider}.js`)));
const target = Object.values(module).find(value => typeof value === 'function')();
const events = [];
const credential = await target.auth.apiKey.login({
  signal: new AbortController().signal,
  prompt: async prompt => { events.push(prompt); return answers.shift(); },
  notify: event => events.push(event),
});
console.log(JSON.stringify({events, credential}));
