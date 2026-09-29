import { DEFAULT_MAX_AGENT_RETRY_DELAY_MS } from "../../pi-ai/sdk-bundle/index.js";
export const DEFAULT_RETRY_POLICY = {
    enabled: true,
    maxRetries: 3,
    baseDelayMs: 1_000,
    maxAgentDelayMs: DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
};
export function validateToolNames(tools) {
    const names = new Set();
    for (const tool of tools) {
        if (names.has(tool.name))
            throw new TypeError(`Duplicate tool name: ${JSON.stringify(tool.name)}`);
        names.add(tool.name);
    }
}
export function validateRetryPolicy(policy) {
    if (!Number.isSafeInteger(policy.maxRetries) ||
        policy.maxRetries < 0 ||
        policy.maxRetries === Number.MAX_SAFE_INTEGER ||
        !Number.isSafeInteger(policy.baseDelayMs) ||
        policy.baseDelayMs < 0 ||
        (policy.maxAgentDelayMs !== undefined &&
            (!Number.isSafeInteger(policy.maxAgentDelayMs) || policy.maxAgentDelayMs < 0))) {
        throw new RangeError("Retry policy values must be finite non-negative safe integers");
    }
}
export function validateCompactionSettings(settings) {
    if (!Number.isSafeInteger(settings.reserveTokens) ||
        settings.reserveTokens < 0 ||
        !Number.isSafeInteger(settings.keepRecentTokens) ||
        settings.keepRecentTokens < 0) {
        throw new RangeError("Compaction token counts must be finite non-negative safe integers");
    }
}
//# sourceMappingURL=config.js.map