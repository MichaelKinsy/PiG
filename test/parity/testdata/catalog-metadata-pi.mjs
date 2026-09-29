const { getModel, getSupportedThinkingLevels } = await import('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js');
const rows = [];
for (const spec of ['anthropic/claude-opus-5-5', 'openai/gpt-6-sol', 'openai-codex/gpt-6-sol', 'openai/gpt-6-luna', 'openai-codex/gpt-6-luna']) {
 const slash = spec.indexOf('/');
 const m = getModel(spec.slice(0, slash), spec.slice(slash + 1));
 const c = m.cost;
 rows.push([spec, getSupportedThinkingLevels(m), m.input, m.input.includes('image'), m.contextWindow, m.maxTokens, [c.input, c.output, c.cacheRead, c.cacheWrite], (c.tiers ?? []).map(t => [t.inputTokensAbove, t.input, t.output, t.cacheRead, t.cacheWrite])]);
}
console.log(JSON.stringify(rows));
