export function getModelSearchText(item) {
    const { id, provider } = item;
    const name = item.name ? ` ${item.name}` : "";
    return `${id} ${provider} ${provider}/${id} ${provider} ${id}${name}`;
}
const splitModelLabel = (value) => value
    .toLowerCase()
    .split(/[^\p{L}\p{N}]+/u)
    .filter(Boolean);
/**
 * Returns the model name when it has words the ID does not, e.g. a renamed override like
 * "MiniMax M3 [rasmus@openrouter, parasail]" for "minimax/minimax-m3". Names that only
 * restate the ID with different casing, separators, or a dropped namespace/version suffix
 * ("Claude Sonnet 4.5 (EU)" for "eu.anthropic.claude-sonnet-4-5-20250929-v1:0") return undefined.
 */
export function getDistinctModelName(item) {
    const name = item.name?.trim();
    if (!name)
        return undefined;
    const idWords = splitModelLabel(item.id);
    const idTokens = new Set(idWords);
    const compactId = idWords.join("");
    // Single-character words must match a whole ID token: "4" is not covered by "mistral-small-2603".
    const isCovered = (word) => idTokens.has(word) || (word.length > 1 && compactId.includes(word));
    return splitModelLabel(name).every(isCovered) ? undefined : name;
}
/**
 * The /model selector search should rank exact provider-prefixed queries before proxy-provider IDs
 * like openrouter/openai/gpt-5, so keep the bare model ID out of the leading position.
 */
export function getModelSelectorSearchText(item) {
    const { id, provider } = item;
    const name = item.name ? ` ${item.name}` : "";
    return `${provider} ${provider}/${id} ${provider} ${id}${name}`;
}
//# sourceMappingURL=model-search.js.map