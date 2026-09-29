// Regenerate: node agent/testdata/tool-validation-oracle.mjs > agent/testdata/tool-validation.json
// Pi 0.87.1 / TypeBox 1.3.27 is the oracle, not a reimplementation of its rules.
import { readFileSync } from 'node:fs';
const pi = new URL('../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/', import.meta.url);
for (const [path, expected] of [['package.json', '0.87.1'], ['node_modules/typebox/package.json', '1.3.27']]) {
  const actual = JSON.parse(readFileSync(new URL(path, pi))).version;
  if (actual !== expected) throw new Error(`${path}: expected ${expected}, got ${actual}`);
}
const { Type, Evaluate, Instantiate } = await import(new URL('node_modules/typebox/build/index.mjs', pi));
const { validateToolArguments } = await import(new URL('node_modules/@earendil-works/pi-ai/dist/utils/validation.js', pi));
const { createReadTool, createBashTool, createEditTool, createWriteTool, createGrepTool, createFindTool, createLsTool, createPowerShellTool } = await import(new URL('dist/core/tools/index.js', pi));

// Non-enumerable TypeBox kinds affect conversion but must never enter provider schemas.
function validationSchema(schema) {
  if (Array.isArray(schema)) return schema.map(validationSchema);
  if (!schema || typeof schema !== 'object') return schema;
  const out = Object.fromEntries(Object.entries(schema).map(([key, value]) => [key, validationSchema(value)]));
  if (schema['~kind']) out['~kind'] = schema['~kind'];
  if (Object.getOwnPropertySymbols(schema).includes(Symbol.for('TypeBox.Kind'))) out['~skipCoercion'] = true;
  if (['Intersect', 'Enum', 'TemplateLiteral'].includes(schema['~kind'])) {
    out['~convert'] = validationSchema(Evaluate(schema['~kind'] === 'Intersect' ? Instantiate({}, schema) : schema));
  }
  return out;
}
const rows = [];
const schemas = [];
const schemaIndices = new Map();
function add(name, tool, args) {
  const schema = validationSchema(tool.parameters);
  const key = JSON.stringify(schema);
  if (!schemaIndices.has(key)) { schemaIndices.set(key, schemas.length); schemas.push(schema); }
  const row = { name, tool: tool.name, schema: schemaIndices.get(key), input: args };
  try { row.output = validateToolArguments(tool, { name: tool.name, arguments: args }); }
  catch (error) { row.error = error.message; }
  rows.push(row);
}
const tools = [createReadTool, createBashTool, createEditTool, createWriteTool, createGrepTool, createFindTool, createLsTool, createPowerShellTool].map(f => f('.'));
const ordinary = {
  read: { path: 'file.txt', offset: 2, limit: 10 }, bash: { command: 'echo ok', timeout: 10 },
  powershell: { command: 'echo ok', timeout: 10 },
  edit: { path: 'file.txt', edits: [{ oldText: 'old', newText: 'new' }] }, write: { path: 'file.txt', content: 'hello' },
  grep: { pattern: 'hello', path: '.', ignoreCase: true, literal: false, context: 2, limit: 10 },
  find: { pattern: '*.txt', path: '.', limit: 10 }, ls: { path: '.', limit: 10 },
};
const values = [null, '', ' ', '10', '10.9', 10.9, '1e2', '0x10', '0b11', true, false, 'TRUE', 'false', '1', '0', 'null', 'undefined', '10n', [], {}, ['2'], 'nonsense'];
for (const tool of tools) {
  add(`${tool.name}/ordinary`, tool, ordinary[tool.name]);
  add(`${tool.name}/missing`, tool, {});
  add(`${tool.name}/root-null`, tool, null);
  add(`${tool.name}/extra`, tool, { ...ordinary[tool.name], extra: { untouched: '10' } });
  for (const property of Object.keys(tool.parameters.properties)) {
    const omitted = { ...ordinary[tool.name] }; delete omitted[property];
    add(`${tool.name}/${property}/omitted`, tool, omitted);
    for (const [index, value] of values.entries()) add(`${tool.name}/${property}/${index}`, tool, { ...ordinary[tool.name], [property]: value });
  }
}
const edit = tools.find(t => t.name === 'edit');
for (const value of values) add(`edit/nested/${JSON.stringify(value)}`, edit, { path: 'file.txt', edits: [{ oldText: value, newText: null }] });
add('edit/nested-missing', edit, { path: 'file.txt', edits: [{}] });
const extensionSchemas = {
  number: Type.Number(), integer: Type.Integer(), boolean: Type.Boolean(), string: Type.String(), null: Type.Null(),
  array: Type.Array(Type.Integer()), tuple: Type.Tuple([Type.Integer(), Type.Boolean()]),
  union: Type.Union([Type.Number(), Type.Null()]), preserve: Type.Union([Type.Number(), Type.String()]),
  literal: Type.Literal(42), enum: Type.Enum(['yes', 'no', 1]),
  nested: Type.Object({ optional: Type.Optional(Type.Number()), required: Type.String() }),
  defaults: Type.Object({ optional: Type.Optional(Type.Number({ default: 99 })) }),
  intersection: Type.Intersect([Type.Object({ x: Type.Integer() }), Type.Object({ y: Type.Boolean() })]),
  additional: Type.Object({}, { additionalProperties: Type.Integer() }),
  record: Type.Record(Type.String(), Type.Integer()),
  oneOf: { oneOf: [{ type: 'integer' }, { type: 'null' }] },
  typeUnion: { type: ['boolean', 'number'] },
  nullableArray: { type: ['array', 'null'], items: { type: 'string' } },
  bounded: Type.Number({ minimum: 3, maximum: 6 }),
  constrained: Type.String({ minLength: 2, maxLength: 4, pattern: '^a' }),
};
for (const [name, schema] of Object.entries(extensionSchemas)) {
  for (const native of [true, false]) {
    const parameters = Type.Object({ value: schema });
    const tool = { name: 'extension', parameters: native ? parameters : JSON.parse(JSON.stringify(parameters)) };
    for (const [index, value] of [...values, { x: '2.9', y: 'TRUE' }, { optional: null, required: null }, { a: '2.9' }, ['2.9', 'TRUE']].entries()) {
      add(`extension/${name}/${native ? 'typebox' : 'plain'}/${index}`, tool, { value });
    }
  }
}
add('read/error-received-json', tools[0], { literal: '\\u003c', html: '<&>', separator: '\u2028', exponent: 1e21 });
for (const value of ['\u0085', '\ufeff10\ufeff', '0x-10', '0x+10', '0x', '1e-999', '1e999', '9007199254740992n']) {
  add(`read/numeric-boundary/${JSON.stringify(value)}`, tools[0], { path: 'file.txt', offset: value });
}
for (const [name, parameters, input] of [
  ['root-union', { anyOf: [{ type: 'object', required: ['value'], properties: { value: { type: 'number' } } }, { type: 'null' }] }, { value: '42' }],
  ['root-union-invalid', { anyOf: [{ type: 'object', required: ['value'], properties: { value: { type: 'number' } } }, { type: 'null' }] }, { value: 'invalid' }],
  ['dependency', { type: 'object', properties: { x: { type: 'string' }, y: { type: 'string' } }, dependentRequired: { x: ['y'] } }, { x: 'x' }],
  ['tuple', { type: 'object', properties: { v: { type: 'array', items: [{ type: 'integer' }, { type: 'boolean' }] } }, required: ['v'] }, { v: ['2', 'true'] }],
  ['ref-invalid', { type: 'object', properties: { value: { $ref: '#/$defs/value' } }, $defs: { value: { type: 'number' } } }, { value: 'invalid' }],
]) add(`extension/${name}`, { name: 'extension', parameters }, input);
const symbolParameters = { type: 'object', properties: { value: { type: 'number' } }, required: ['value'] };
Object.defineProperty(symbolParameters, Symbol.for('TypeBox.Kind'), { value: 'Object' });
add('extension/symbol-kind-skips-plain-coercion', { name: 'extension', parameters: symbolParameters }, { value: '42' });
process.stdout.write('{"schemas":' + JSON.stringify(schemas, null, 2) + ',"cases":[\n' + rows.map(row => JSON.stringify(row)).join(',\n') + '\n]}\n');
