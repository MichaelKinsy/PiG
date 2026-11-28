/** Assistant message fields that carry plain reasoning text, in the order the stream prefers them. */
export const OPENAI_COMPLETIONS_REASONING_FIELDS = ["reasoning_content", "reasoning", "reasoning_text"];
/** Decodes a stored `thinkingSignature`; undefined when it is empty or not one of the known formats. */
export function decodeThinkingSignature(signature) {
    if (!signature)
        return undefined;
    if (isOpenAICompletionsReasoningField(signature))
        return { kind: "field", field: signature };
    // This runs for every thinking block of every request. Only the JSON formats start with these characters;
    // parsing anything else would throw, which costs far more than this prefix check.
    switch (signature[0]) {
        case "[":
            return decodeDetailsSignature(signature);
        case "{":
            return decodeOpaqueSignature(signature);
        default:
            return undefined;
    }
}
/** Encodes replay data for `thinkingSignature`, in the format `decodeThinkingSignature` reads. */
export function encodeThinkingSignature(signature) {
    switch (signature.kind) {
        case "field":
            return signature.field;
        case "details":
            return JSON.stringify(signature.details);
        case "opaque":
            return JSON.stringify({ field: signature.field, reasoning_opaque: signature.opaque });
    }
}
function isOpenAICompletionsReasoningField(field) {
    return OPENAI_COMPLETIONS_REASONING_FIELDS.includes(field);
}
function decodeDetailsSignature(signature) {
    const parsed = parseJson(signature);
    return Array.isArray(parsed) && parsed.length > 0 && parsed.every(isOpenAIReasoningDetail)
        ? { kind: "details", details: parsed }
        : undefined;
}
function decodeOpaqueSignature(signature) {
    const parsed = parseJson(signature);
    if (!isJsonObject(parsed))
        return undefined;
    const { field, reasoning_opaque: opaque } = parsed;
    if (typeof opaque !== "string" || opaque.length === 0)
        return undefined;
    if (field === undefined)
        return { kind: "opaque", opaque };
    return typeof field === "string" && isOpenAICompletionsReasoningField(field)
        ? { kind: "opaque", field, opaque }
        : undefined;
}
function parseJson(text) {
    try {
        return JSON.parse(text);
    }
    catch {
        return undefined;
    }
}
function isJsonObject(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
/** Whether a wire or stored value is a well-formed `reasoning_details` entry. */
export function isOpenAIReasoningDetail(detail) {
    if (!isJsonObject(detail) || !hasValidCommonReasoningDetailFields(detail)) {
        return false;
    }
    switch (detail.type) {
        case "reasoning.summary":
            return typeof detail.summary === "string";
        case "reasoning.encrypted":
            return typeof detail.data === "string";
        case "reasoning.text":
            return (typeof detail.text === "string" &&
                (detail.signature === undefined || detail.signature === null || typeof detail.signature === "string"));
        default:
            return false;
    }
}
function hasValidCommonReasoningDetailFields(candidate) {
    return ((candidate.id === undefined || candidate.id === null || typeof candidate.id === "string") &&
        (candidate.format === undefined || typeof candidate.format === "string") &&
        (candidate.index === undefined || typeof candidate.index === "number"));
}
/**
 * Appends a streamed `reasoning_details` delta. OpenRouter streams them as deltas: consecutive text and summary
 * deltas merge into one logical entry, while encrypted entries stay opaque and discrete.
 */
export function appendOpenAIReasoningDetail(details, detail) {
    const lastDetail = details[details.length - 1];
    if (detail.type === "reasoning.text" && lastDetail?.type === "reasoning.text") {
        lastDetail.text += detail.text;
        lastDetail.signature ||= detail.signature;
        fillMissingCommonReasoningDetailFields(lastDetail, detail);
        return;
    }
    if (detail.type === "reasoning.summary" && lastDetail?.type === "reasoning.summary") {
        lastDetail.summary += detail.summary;
        fillMissingCommonReasoningDetailFields(lastDetail, detail);
        return;
    }
    details.push({ ...detail });
}
function fillMissingCommonReasoningDetailFields(target, source) {
    target.id ??= source.id;
    target.format ||= source.format;
    target.index ??= source.index;
}
/**
 * Reads the encrypted `reasoning_details` entry that older sessions stored in a tool call's `thoughtSignature`
 * before details moved to the thinking block.
 */
export function decodeLegacyToolCallReasoningDetail(thoughtSignature) {
    if (!thoughtSignature?.startsWith("{"))
        return undefined;
    const parsed = parseJson(thoughtSignature);
    return isOpenAIReasoningDetail(parsed) &&
        parsed.type === "reasoning.encrypted" &&
        typeof parsed.id === "string" &&
        parsed.id.length > 0 &&
        parsed.data.length > 0
        ? parsed
        : undefined;
}
//# sourceMappingURL=openai-completions-reasoning.js.map