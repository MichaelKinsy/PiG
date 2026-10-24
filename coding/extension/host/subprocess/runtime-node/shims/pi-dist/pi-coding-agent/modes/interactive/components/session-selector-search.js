import { fuzzyMatch } from "../../../../../pi-tui.mjs";
function normalizeWhitespaceLower(text) {
    return text.toLowerCase().replace(/\s+/g, " ").trim();
}
/**
 * Match tiers, best first. Each token is scored by the best field it matches; tiers dominate
 * fuzzy scores so an exact hit anywhere beats a scattered subsequence in a short field.
 */
const TIER_NAME = 0; // query text appears in the session name
const TIER_TEXT = 1; // query text appears in the session id, messages, or cwd
const TIER_FUZZY = 2; // query characters appear scattered in the session name or cwd
const TIER_WEIGHT = 1_000_000;
/**
 * Lowercased message text per session. allMessagesText holds every user/assistant message and
 * can be megabytes, so lowercase it once instead of on every keystroke.
 */
const messageTextCache = new WeakMap();
function getMessageText(session) {
    let cached = messageTextCache.get(session);
    if (!cached || cached.source !== session.allMessagesText) {
        cached = { source: session.allMessagesText, lower: session.allMessagesText.toLowerCase() };
        messageTextCache.set(session, cached);
    }
    return cached;
}
function getNormalizedMessageText(session) {
    const cached = getMessageText(session);
    cached.normalized ??= cached.lower.replace(/\s+/g, " ").trim();
    return cached.normalized;
}
function tierScore(tier, score = 0) {
    return tier * TIER_WEIGHT + score;
}
/** Exact (case-insensitive) text match. Messages and ids are only matched exactly: scattered matches in them are noise. */
function matchLiteralToken(session, value) {
    const name = session.name?.toLowerCase() ?? "";
    if (name.includes(value))
        return tierScore(TIER_NAME, fuzzyMatch(value, name).score);
    if (session.id.toLowerCase().includes(value))
        return tierScore(TIER_TEXT);
    if (getMessageText(session).lower.includes(value))
        return tierScore(TIER_TEXT);
    if (session.cwd.toLowerCase().includes(value))
        return tierScore(TIER_TEXT);
    return undefined;
}
function matchFuzzyToken(session, value) {
    const literal = matchLiteralToken(session, value.toLowerCase());
    if (literal !== undefined)
        return literal;
    let best;
    for (const field of [session.name ?? "", session.cwd]) {
        const m = fuzzyMatch(value, field);
        if (m.matches && (best === undefined || m.score < best))
            best = m.score;
    }
    return best === undefined ? undefined : tierScore(TIER_FUZZY, best);
}
function matchPhraseToken(session, phrase) {
    const name = normalizeWhitespaceLower(session.name ?? "");
    if (name.includes(phrase))
        return tierScore(TIER_NAME, fuzzyMatch(phrase, name).score);
    if (session.id.toLowerCase().includes(phrase))
        return tierScore(TIER_TEXT);
    if (getNormalizedMessageText(session).includes(phrase))
        return tierScore(TIER_TEXT);
    if (normalizeWhitespaceLower(session.cwd).includes(phrase))
        return tierScore(TIER_TEXT);
    return undefined;
}
function matchRegex(session, regex) {
    if (session.name && regex.test(session.name))
        return tierScore(TIER_NAME);
    if (regex.test(session.id) || regex.test(session.allMessagesText) || regex.test(session.cwd)) {
        return tierScore(TIER_TEXT);
    }
    return undefined;
}
export function hasSessionName(session) {
    return Boolean(session.name?.trim());
}
function matchesNameFilter(session, filter) {
    if (filter === "all")
        return true;
    return hasSessionName(session);
}
export function parseSearchQuery(query) {
    const trimmed = query.trim();
    if (!trimmed) {
        return { mode: "tokens", tokens: [], regex: null };
    }
    // Regex mode: re:<pattern>
    if (trimmed.startsWith("re:")) {
        const pattern = trimmed.slice(3).trim();
        if (!pattern) {
            return { mode: "regex", tokens: [], regex: null, error: "Empty regex" };
        }
        try {
            return { mode: "regex", tokens: [], regex: new RegExp(pattern, "i") };
        }
        catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            return { mode: "regex", tokens: [], regex: null, error: msg };
        }
    }
    // Token mode with quote support.
    // Example: foo "node cve" bar
    const tokens = [];
    let buf = "";
    let inQuote = false;
    let hadUnclosedQuote = false;
    const flush = (kind) => {
        const v = buf.trim();
        buf = "";
        if (!v)
            return;
        tokens.push({ kind, value: v });
    };
    for (let i = 0; i < trimmed.length; i++) {
        const ch = trimmed[i];
        if (ch === '"') {
            if (inQuote) {
                flush("phrase");
                inQuote = false;
            }
            else {
                flush("fuzzy");
                inQuote = true;
            }
            continue;
        }
        if (!inQuote && /\s/.test(ch)) {
            flush("fuzzy");
            continue;
        }
        buf += ch;
    }
    if (inQuote) {
        hadUnclosedQuote = true;
    }
    // If quotes were unbalanced, fall back to plain whitespace tokenization.
    if (hadUnclosedQuote) {
        return {
            mode: "tokens",
            tokens: trimmed
                .split(/\s+/)
                .map((t) => t.trim())
                .filter((t) => t.length > 0)
                .map((t) => ({ kind: "fuzzy", value: t })),
            regex: null,
        };
    }
    flush(inQuote ? "phrase" : "fuzzy");
    return { mode: "tokens", tokens, regex: null };
}
/**
 * Each field is matched separately, so a token never matches across field boundaries.
 * Different tokens may match different fields.
 */
export function matchSession(session, parsed) {
    if (parsed.mode === "regex") {
        if (!parsed.regex) {
            return { matches: false, score: 0 };
        }
        const score = matchRegex(session, parsed.regex);
        return score === undefined ? { matches: false, score: 0 } : { matches: true, score };
    }
    let totalScore = 0;
    for (const token of parsed.tokens) {
        let score;
        if (token.kind === "phrase") {
            const phrase = normalizeWhitespaceLower(token.value);
            if (!phrase)
                continue;
            score = matchPhraseToken(session, phrase);
        }
        else {
            score = matchFuzzyToken(session, token.value);
        }
        if (score === undefined)
            return { matches: false, score: 0 };
        totalScore += score;
    }
    return { matches: true, score: totalScore };
}
export function filterAndSortSessions(sessions, query, sortMode, nameFilter = "all") {
    const nameFiltered = nameFilter === "all" ? sessions : sessions.filter((session) => matchesNameFilter(session, nameFilter));
    const trimmed = query.trim();
    if (!trimmed)
        return nameFiltered;
    const parsed = parseSearchQuery(query);
    if (parsed.error)
        return [];
    // Recent mode: filter only, keep incoming order.
    if (sortMode === "recent") {
        const filtered = [];
        for (const s of nameFiltered) {
            const res = matchSession(s, parsed);
            if (res.matches)
                filtered.push(s);
        }
        return filtered;
    }
    // Relevance mode: sort by score, tie-break by modified desc.
    const scored = [];
    for (const s of nameFiltered) {
        const res = matchSession(s, parsed);
        if (!res.matches)
            continue;
        scored.push({ session: s, score: res.score });
    }
    scored.sort((a, b) => {
        if (a.score !== b.score)
            return a.score - b.score;
        return b.session.modified.getTime() - a.session.modified.getTime();
    });
    return scored.map((r) => r.session);
}
//# sourceMappingURL=session-selector-search.js.map