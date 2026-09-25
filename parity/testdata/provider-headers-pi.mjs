#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const source = readFileSync('.upstream/current/packages/coding-agent/src/core/model-runtime.ts', 'utf8');
const start = source.indexOf('function mergeHeaders(');
const end = source.indexOf('/** Configured pi-ai Models collection', start);
if (start < 0 || end < 0) throw new Error('cannot locate pinned mergeHeaders source');
const definition = source.slice(start, end).trim();
const bodyStart = definition.indexOf('): ProviderHeaders | undefined {');
const bodyEnd = definition.lastIndexOf('}');
if (bodyStart < 0 || bodyEnd <= bodyStart) throw new Error('unexpected mergeHeaders source shape');
const body = definition.slice(bodyStart + '): ProviderHeaders | undefined {'.length, bodyEnd);
const mergeHeaders = new Function('base', 'override', body);

const versionSource = readFileSync('coding/pigversion/pigversion.go', 'utf8');
const versionMatch = versionSource.match(/UpstreamVersion = "([^"]+)"/);
if (!versionMatch) throw new Error('cannot read UpstreamVersion');
const packageRoot = process.env.PI_PACKAGE_ROOT ?? join(homedir(), '.local/share/mise/installs/npm-earendil-works-pi-coding-agent', versionMatch[1], 'lib/node_modules/@earendil-works/pi-coding-agent');
const headersModule = await import(pathToFileURL(join(packageRoot, 'node_modules/@earendil-works/pi-ai/dist/utils/headers.js')).href);

const cases = [
  { name: 'delete-case-insensitive', base: { Authorization: 'base', 'X-Keep': 'yes' }, override: { authorization: null, 'x-new': 'new' } },
  { name: 'replace-case-insensitive', base: { 'X-Test': 'old' }, override: { 'x-test': 'new' } },
  { name: 'delete-last-header', base: { 'X-Only': 'value' }, override: { 'x-only': null } },
  { name: 'undefined-input', base: undefined, override: undefined },
];

const output = cases.map((testCase) => {
  const resolved = headersModule.providerHeadersToRecord(mergeHeaders(testCase.base, testCase.override));
  return {
    name: testCase.name,
    headers: Object.entries(resolved ?? {}).sort(([left], [right]) => left.localeCompare(right)),
  };
});
process.stdout.write(`${JSON.stringify(output)}\n`);
