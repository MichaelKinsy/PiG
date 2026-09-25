import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
const { encodeControlLine, MAX_CONTROL_LINE_BYTES } = await import(pathToFileURL(`${process.env.PIG_TEST_ROOT}/.upstream/current/packages/coding-agent/src/experimental/process.ts`));
console.log(MAX_CONTROL_LINE_BYTES);
for (const line of readFileSync(0, 'utf8').trimEnd().split('\n')) {
  process.stdout.write(encodeControlLine(JSON.parse(line)));
}
