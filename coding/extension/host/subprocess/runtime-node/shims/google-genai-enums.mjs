// PiG (D74): the enums api/google-shared.js imports from @google/genai 3.6.1, copied byte for byte from its dist/node/index.mjs by automation/gen/vendor-pi-ai-apis.mjs.
// The rest of the SDK is not shipped to extension processes.
/**
 * @license
 * Copyright 2025 Google LLC
 * SPDX-License-Identifier: Apache-2.0
 */

var FinishReason;
(function (FinishReason) {
    /**
     * The finish reason is unspecified.
     */
    FinishReason["FINISH_REASON_UNSPECIFIED"] = "FINISH_REASON_UNSPECIFIED";
    /**
     * The model reached a natural stopping point or a configured stop sequence.
     */
    FinishReason["STOP"] = "STOP";
    /**
     * The model generated the maximum number of tokens allowed by the `max_output_tokens` parameter.
     */
    FinishReason["MAX_TOKENS"] = "MAX_TOKENS";
    /**
     * The model stopped generating because the content potentially violates safety policies. NOTE: When streaming, the `content` field is empty if content filters block the output.
     */
    FinishReason["SAFETY"] = "SAFETY";
    /**
     * The model stopped generating because the content may be a recitation from a source.
     */
    FinishReason["RECITATION"] = "RECITATION";
    /**
     * The response candidate content was flagged for using an unsupported language. This enum value is not supported in Vertex AI.
     */
    FinishReason["LANGUAGE"] = "LANGUAGE";
    /**
     * The model stopped generating for a reason not otherwise specified.
     */
    FinishReason["OTHER"] = "OTHER";
    /**
     * The model stopped generating because the content contains a term from a configured blocklist.
     */
    FinishReason["BLOCKLIST"] = "BLOCKLIST";
    /**
     * The model stopped generating because the content may be prohibited.
     */
    FinishReason["PROHIBITED_CONTENT"] = "PROHIBITED_CONTENT";
    /**
     * The model stopped generating because the content may contain sensitive personally identifiable information (SPII).
     */
    FinishReason["SPII"] = "SPII";
    /**
     * The model generated a function call that is syntactically invalid and can't be parsed.
     */
    FinishReason["MALFORMED_FUNCTION_CALL"] = "MALFORMED_FUNCTION_CALL";
    /**
     * The generated image potentially violates safety policies.
     */
    FinishReason["IMAGE_SAFETY"] = "IMAGE_SAFETY";
    /**
     * The generated image may contain prohibited content.
     */
    FinishReason["IMAGE_PROHIBITED_CONTENT"] = "IMAGE_PROHIBITED_CONTENT";
    /**
     * The image generation stopped for a reason not otherwise specified.
     */
    FinishReason["IMAGE_OTHER"] = "IMAGE_OTHER";
    /**
     * The model was expected to generate an image, but didn't.
     */
    FinishReason["NO_IMAGE"] = "NO_IMAGE";
    /**
     * The generated image may be a recitation from a source.
     */
    FinishReason["IMAGE_RECITATION"] = "IMAGE_RECITATION";
    /**
     * The model generated a function call that is semantically invalid. This can happen, for example, if function calling is not enabled or the generated function is not in the function declaration.
     */
    FinishReason["UNEXPECTED_TOOL_CALL"] = "UNEXPECTED_TOOL_CALL";
    /**
     * The model made too many tool calls in a single request, so the server-side tool loop stopped before the model returned a final answer.
     */
    FinishReason["TOO_MANY_TOOL_CALLS"] = "TOO_MANY_TOOL_CALLS";
    /**
     * Request has at least one thought signature missing. This enum value is not supported in Vertex AI.
     */
    FinishReason["MISSING_THOUGHT_SIGNATURE"] = "MISSING_THOUGHT_SIGNATURE";
    /**
     * Finishes due to malformed response.
     */
    FinishReason["MALFORMED_RESPONSE"] = "MALFORMED_RESPONSE";
    /**
     * Request was filtered by an escalation rule. This enum value is not supported in Vertex AI.
     */
    FinishReason["ESCALATION"] = "ESCALATION";
    /**
     * Indicates that token generation stopped because the user account is limited or disabled due to Prohibited Use Policy (PUP) violations. This enum value is not supported in Vertex AI.
     */
    FinishReason["PUP_LIMITED_DISABLED"] = "PUP_LIMITED_DISABLED";
    /**
     * Token generation was stopped because the response reached the per-request token limit, but generation is not yet complete. The model response can be continued via a subsequent request using the returned `continuation_token`.
     */
    FinishReason["CONTINUATION"] = "CONTINUATION";
    /**
     * The model response was blocked by Model Armor. This enum value is not supported in Gemini API.
     */
    FinishReason["MODEL_ARMOR"] = "MODEL_ARMOR";
})(FinishReason || (FinishReason = {}));

var FunctionCallingConfigMode;
(function (FunctionCallingConfigMode) {
    /**
     * Unspecified function calling mode. This value should not be used.
     */
    FunctionCallingConfigMode["MODE_UNSPECIFIED"] = "MODE_UNSPECIFIED";
    /**
     * Default model behavior, model decides to predict either function calls or natural language response.
     */
    FunctionCallingConfigMode["AUTO"] = "AUTO";
    /**
     * Model is constrained to always predicting function calls only. If "allowed_function_names" are set, the predicted function calls will be limited to any one of "allowed_function_names", else the predicted function calls will be any one of the provided "function_declarations".
     */
    FunctionCallingConfigMode["ANY"] = "ANY";
    /**
     * Model will not predict any function calls. Model behavior is same as when not passing any function declarations.
     */
    FunctionCallingConfigMode["NONE"] = "NONE";
    /**
     * Model is constrained to predict either function calls or natural language response. If "allowed_function_names" are set, the predicted function calls will be limited to any one of "allowed_function_names", else the predicted function calls will be any one of the provided "function_declarations".
     */
    FunctionCallingConfigMode["VALIDATED"] = "VALIDATED";
})(FunctionCallingConfigMode || (FunctionCallingConfigMode = {}));

var ThinkingLevel;
(function (ThinkingLevel) {
    /**
     * Unspecified thinking level.
     */
    ThinkingLevel["THINKING_LEVEL_UNSPECIFIED"] = "THINKING_LEVEL_UNSPECIFIED";
    /**
     * MINIMAL thinking level.
     */
    ThinkingLevel["MINIMAL"] = "MINIMAL";
    /**
     * Low thinking level.
     */
    ThinkingLevel["LOW"] = "LOW";
    /**
     * Medium thinking level.
     */
    ThinkingLevel["MEDIUM"] = "MEDIUM";
    /**
     * High thinking level.
     */
    ThinkingLevel["HIGH"] = "HIGH";
    /**
     * XHIGH thinking level.
     */
    ThinkingLevel["XHIGH"] = "XHIGH";
})(ThinkingLevel || (ThinkingLevel = {}));

export { FinishReason, FunctionCallingConfigMode, ThinkingLevel };
