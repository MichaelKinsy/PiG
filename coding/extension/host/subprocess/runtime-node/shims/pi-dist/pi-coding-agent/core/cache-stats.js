/**
 * Prompt-cache TTL: idle gaps longer than this are worth mentioning as the
 * likely cause of a miss. Anthropic's default cache TTL is 5 minutes.
 */
export const CACHE_TTL_MS = 5 * 60 * 1000;
/** Per-turn misses at or below this are cache breakpoint granularity noise. */
const NOISE_FLOOR_TOKENS = 1024;
/**
 * Compute the cache miss for one assistant message relative to the previous
 * request. Returns undefined when nothing is counted: first turn, after a
 * reset, no cache activity ever reported (provider without cache support), or
 * miss below the noise floor.
 */
function detectMiss(prev, message, models) {
    const usage = message.usage;
    const promptTokens = usage.input + usage.cacheRead + usage.cacheWrite;
    // A zero-cache turn only counts when cache activity was reported before:
    // on cache-read-only providers that is a total miss, while on providers
    // that never report caching it means nothing.
    if (!prev || promptTokens <= 0 || (usage.cacheRead + usage.cacheWrite === 0 && !prev.reportedCache)) {
        return undefined;
    }
    const missedTokens = Math.min(prev.promptTokens, promptTokens) - usage.cacheRead;
    if (missedTokens <= NOISE_FLOOR_TOKENS)
        return undefined;
    // Extra cost = missed tokens billed at the actual paid rate (input/cacheWrite,
    // incl. write premium) instead of the cache-read rate. Missed tokens can only
    // land in the input or cacheWrite buckets, so the paid rate comes straight
    // from this message's own cost breakdown.
    const paidTokens = usage.input + usage.cacheWrite;
    const paidPerToken = paidTokens > 0 ? (usage.cost.input + usage.cost.cacheWrite) / paidTokens : 0;
    const readPerToken = usage.cacheRead > 0
        ? usage.cost.cacheRead / usage.cacheRead
        : (models.getModel(message.provider, message.model)?.cost.cacheRead ?? 0) / 1_000_000;
    return {
        missedTokens,
        missedCost: missedTokens * Math.max(0, paidPerToken - readPerToken),
        idleMs: Math.max(0, message.timestamp - prev.timestamp),
        modelChanged: `${message.provider}/${message.model}` !== prev.modelKey,
    };
}
function asPreviousRequest(message, reportedCache) {
    const usage = message.usage;
    const promptTokens = usage.input + usage.cacheRead + usage.cacheWrite;
    if (promptTokens <= 0)
        return undefined;
    return {
        promptTokens,
        modelKey: `${message.provider}/${message.model}`,
        timestamp: message.timestamp,
        reportedCache: reportedCache || usage.cacheRead + usage.cacheWrite > 0,
    };
}
function scan(entries, models) {
    let prev;
    const totals = { missedTokens: 0, missedCost: 0, missCount: 0 };
    const misses = new Map();
    for (const entry of entries) {
        if (entry.type === "compaction" || entry.type === "branch_summary") {
            // The context legitimately changed; the next turn's prompt is new content,
            // not re-billed content. Model switches are NOT exempt: they re-bill the
            // full prompt and should be counted.
            prev = undefined;
            continue;
        }
        if (entry.type === "usage" && entry.kind === "cache_warm") {
            const promptTokens = entry.usage.input + entry.usage.cacheRead + entry.usage.cacheWrite;
            if (promptTokens > 0) {
                prev = {
                    promptTokens,
                    modelKey: `${entry.provider}/${entry.model}`,
                    timestamp: Date.parse(entry.timestamp),
                    reportedCache: true,
                };
            }
        }
        else if (entry.type === "message" && entry.message.role === "assistant") {
            const miss = detectMiss(prev, entry.message, models);
            if (miss) {
                totals.missedTokens += miss.missedTokens;
                totals.missedCost += miss.missedCost;
                totals.missCount += 1;
                misses.set(entry.message, miss);
            }
            prev = asPreviousRequest(entry.message, prev?.reportedCache ?? false) ?? prev;
        }
    }
    return { prev, totals, misses };
}
/**
 * Cumulative cache waste across a session: prompt tokens that should have been
 * cache reads (they were in the previous turn's prompt) but were re-billed.
 */
export function computeCacheWaste(entries, models) {
    return scan(entries, models).totals;
}
/**
 * All counted cache misses across a session, keyed by the assistant message
 * (by reference) that paid for them. Used to re-derive transcript notices when
 * rebuilding the chat from entries (resume, post-compaction rebuild).
 */
export function collectCacheMisses(entries, models) {
    return scan(entries, models).misses;
}
/**
 * Detect a cache miss on a just-completed assistant message.
 * `entries` must not yet contain `message` (message_end fires before persistence).
 */
export function detectCacheMiss(entries, message, models) {
    return detectMiss(scan(entries, models).prev, message, models);
}
//# sourceMappingURL=cache-stats.js.map