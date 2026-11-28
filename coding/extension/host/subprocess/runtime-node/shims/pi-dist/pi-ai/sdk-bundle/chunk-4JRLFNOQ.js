import "./chunk-EENKJTF4.js";
import "./chunk-HFMBG2Z5.js";
import "./chunk-UVW3US7A.js";
import "./chunk-EDPW3NGU.js";
import {
  resolveTranscript,
  resolveTranscriptTools
} from "./chunk-5FR3CSGH.js";
import {
  getSystemMessageText,
  renderSystemMessageUpdate
} from "./chunk-5ASU6EDP.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/openai-completions.js
import { bridgeApi } from "../../../pi-ai-bridge.mjs";

// pi-dist/pi-ai/api/openai-completions.impl.js
import OpenAI from "../../../openai-sdk-absent.mjs";
import { AssistantMessageEventStream } from "../utils/event-stream.js";

// pi-dist/pi-ai/utils/hash.js
function shortHash(str) {
  let h1 = 3735928559;
  let h2 = 1103547991;
  for (let i = 0; i < str.length; i++) {
    const ch = str.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ h1 >>> 16, 2246822507) ^ Math.imul(h2 ^ h2 >>> 13, 3266489909);
  h2 = Math.imul(h2 ^ h2 >>> 16, 2246822507) ^ Math.imul(h1 ^ h1 >>> 13, 3266489909);
  return (h2 >>> 0).toString(36) + (h1 >>> 0).toString(36);
}
__name(shortHash, "shortHash");

// pi-dist/pi-ai/api/openai-completions.impl.js
import { getProviderEnvValue } from "../utils/provider-env.js";

// pi-dist/pi-ai/utils/sanitize-unicode.js
function sanitizeSurrogates(text) {
  return text.replace(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g, "");
}
__name(sanitizeSurrogates, "sanitizeSurrogates");

// pi-dist/pi-ai/api/constrained-sampling.js
var UnsupportedStrictJsonSchemaError = class extends Error {
  static {
    __name(this, "UnsupportedStrictJsonSchemaError");
  }
};
var UNSUPPORTED_STRICT_SCHEMA_KEYS = [
  "$ref",
  "$defs",
  "definitions",
  "allOf",
  "oneOf",
  "patternProperties",
  "dependentSchemas",
  "dependencies",
  "unevaluatedProperties",
  "propertyNames",
  "contains",
  "prefixItems",
  "not",
  "if",
  "then",
  "else"
];
function isJsonSchemaObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isJsonSchemaObject, "isJsonSchemaObject");
function isStructuredSchema(schema) {
  if (!isJsonSchemaObject(schema))
    return false;
  const types = typeof schema.type === "string" ? [schema.type] : Array.isArray(schema.type) ? schema.type : [];
  return types.includes("object") || types.includes("array") || schema.properties !== void 0 || schema.items !== void 0;
}
__name(isStructuredSchema, "isStructuredSchema");
function schemaAllowsNull(schema) {
  if (!isJsonSchemaObject(schema))
    return false;
  if (schema.type === "null" || Array.isArray(schema.type) && schema.type.includes("null"))
    return true;
  if (schema.const === null || Array.isArray(schema.enum) && schema.enum.includes(null))
    return true;
  return Array.isArray(schema.anyOf) && schema.anyOf.some((variant) => schemaAllowsNull(variant));
}
__name(schemaAllowsNull, "schemaAllowsNull");
function makeJsonSchemaNodeStrict(schema, isUnsupportedKeyword) {
  if (!isJsonSchemaObject(schema)) {
    throw new UnsupportedStrictJsonSchemaError("boolean schemas are unsupported");
  }
  for (const key of UNSUPPORTED_STRICT_SCHEMA_KEYS) {
    if (schema[key] !== void 0) {
      throw new UnsupportedStrictJsonSchemaError(`${key} schemas are unsupported`);
    }
  }
  if (isUnsupportedKeyword) {
    for (const [key, value] of Object.entries(schema)) {
      if (isUnsupportedKeyword(key, value)) {
        throw new UnsupportedStrictJsonSchemaError(`${key}: ${JSON.stringify(value)} is unsupported`);
      }
    }
  }
  if (schema.type === void 0 && schema.anyOf === void 0 && schema.enum === void 0) {
    throw new UnsupportedStrictJsonSchemaError("schemas without a type are unsupported");
  }
  if (schema.anyOf !== void 0) {
    if (!Array.isArray(schema.anyOf) || schema.anyOf.length === 0) {
      throw new UnsupportedStrictJsonSchemaError("anyOf must contain at least one schema");
    }
    for (const variant of schema.anyOf) {
      if (isStructuredSchema(variant)) {
        throw new UnsupportedStrictJsonSchemaError("object and array unions are unsupported");
      }
      makeJsonSchemaNodeStrict(variant, isUnsupportedKeyword);
    }
  }
  if (schema.items !== void 0) {
    if (Array.isArray(schema.items)) {
      throw new UnsupportedStrictJsonSchemaError("tuple schemas are unsupported");
    }
    makeJsonSchemaNodeStrict(schema.items, isUnsupportedKeyword);
  }
  const isObjectSchema = schema.type === "object";
  if (schema.properties !== void 0 && !isObjectSchema) {
    throw new UnsupportedStrictJsonSchemaError("properties require type object");
  }
  if (!isObjectSchema)
    return;
  if (schema.additionalProperties !== void 0 && schema.additionalProperties !== false) {
    throw new UnsupportedStrictJsonSchemaError("schema-valued or true additionalProperties is unsupported");
  }
  if (schema.properties !== void 0 && !isJsonSchemaObject(schema.properties)) {
    throw new UnsupportedStrictJsonSchemaError("object properties must be a schema map");
  }
  if (schema.required !== void 0 && (!Array.isArray(schema.required) || schema.required.some((key) => typeof key !== "string"))) {
    throw new UnsupportedStrictJsonSchemaError("object required must be a string array");
  }
  const properties = schema.properties ?? {};
  const propertyNames = Object.keys(properties);
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  if ([...required].some((key) => !propertyNames.includes(key))) {
    throw new UnsupportedStrictJsonSchemaError("required contains an unknown property");
  }
  for (const [key, property] of Object.entries(properties)) {
    makeJsonSchemaNodeStrict(property, isUnsupportedKeyword);
    if (!required.has(key) && !schemaAllowsNull(property)) {
      properties[key] = { anyOf: [property, { type: "null" }] };
    }
  }
  schema.required = propertyNames;
  schema.additionalProperties = false;
}
__name(makeJsonSchemaNodeStrict, "makeJsonSchemaNodeStrict");
function makeStrictJsonSchema(schema, isUnsupportedKeyword) {
  const cloned = structuredClone(schema);
  if (!isJsonSchemaObject(cloned)) {
    throw new UnsupportedStrictJsonSchemaError("root schema must have type object");
  }
  makeJsonSchemaNodeStrict(cloned, isUnsupportedKeyword);
  if (cloned.type !== "object") {
    throw new UnsupportedStrictJsonSchemaError("root schema must have type object");
  }
  return cloned;
}
__name(makeStrictJsonSchema, "makeStrictJsonSchema");
function getJsonSchemaToolParameters(tool, strict) {
  return strict === true ? makeStrictJsonSchema(tool.parameters) : tool.parameters;
}
__name(getJsonSchemaToolParameters, "getJsonSchemaToolParameters");
function getGrammarToolInput(toolName, arguments_, inputProperty) {
  const input = arguments_[inputProperty];
  if (typeof input !== "string") {
    throw new Error(`Grammar tool call "${toolName}" requires argument "${inputProperty}" to be a string.`);
  }
  return input;
}
__name(getGrammarToolInput, "getGrammarToolInput");
function inferGrammarInputProperty(tool) {
  const schema = tool.parameters;
  if (schema.type !== "object") {
    throw new Error("grammar constrained sampling requires an object parameter schema");
  }
  if (!Array.isArray(schema.required) || schema.required.length !== 1 || typeof schema.required[0] !== "string") {
    throw new Error("grammar constrained sampling requires exactly one required string property");
  }
  const inputProperty = schema.required[0];
  if (!schema.properties?.[inputProperty]) {
    throw new Error(`grammar constrained sampling requires a properties entry for ${inputProperty}`);
  }
  if (schema.properties[inputProperty]?.type !== "string") {
    throw new Error(`grammar constrained sampling property ${inputProperty} must have type string`);
  }
  return inputProperty;
}
__name(inferGrammarInputProperty, "inferGrammarInputProperty");
function resolveJsonSchemaStrictSampling(tool, supportsStrictMode, isUnsupportedKeyword) {
  const config = tool.constrainedSampling;
  if (!config || config.type !== "json_schema")
    return void 0;
  if (supportsStrictMode) {
    try {
      makeStrictJsonSchema(tool.parameters, isUnsupportedKeyword);
      return true;
    } catch (error) {
      if (!(error instanceof UnsupportedStrictJsonSchemaError))
        throw error;
      if (config.strict !== "require")
        return void 0;
      throw new Error(`Tool "${tool.name}" requires JSON-schema constrained sampling, but ${error.message}.`);
    }
  }
  if (config.strict === "require") {
    throw new Error(`Tool "${tool.name}" requires JSON-schema constrained sampling, but strict tools are unsupported.`);
  }
  return void 0;
}
__name(resolveJsonSchemaStrictSampling, "resolveJsonSchemaStrictSampling");
function resolveGrammarConstrainedSampling(tool, supportsOpenAIGrammarTools) {
  const config = tool.constrainedSampling;
  if (!config || config.type !== "grammar") {
    return void 0;
  }
  if (!supportsOpenAIGrammarTools) {
    return void 0;
  }
  const larkDefinition = config.variants.openai_lark;
  const regexDefinition = config.variants.openai_regex;
  const hasLarkDefinition = typeof larkDefinition === "string" && larkDefinition.trim().length > 0;
  const hasRegexDefinition = typeof regexDefinition === "string" && regexDefinition.trim().length > 0;
  if (!hasLarkDefinition && !hasRegexDefinition) {
    throw new Error(`Tool "${tool.name}" cannot use grammar constrained sampling: no supported grammar variant was provided.`);
  }
  try {
    return {
      format: hasLarkDefinition ? "lark" : "regex",
      definition: hasLarkDefinition ? larkDefinition : regexDefinition,
      inputProperty: inferGrammarInputProperty(tool)
    };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`Tool "${tool.name}" cannot use grammar constrained sampling: ${message}.`);
  }
}
__name(resolveGrammarConstrainedSampling, "resolveGrammarConstrainedSampling");

// pi-dist/pi-ai/api/openai-completions-reasoning.js
var OPENAI_COMPLETIONS_REASONING_FIELDS = ["reasoning_content", "reasoning", "reasoning_text"];
function decodeThinkingSignature(signature) {
  if (!signature)
    return void 0;
  if (isOpenAICompletionsReasoningField(signature))
    return { kind: "field", field: signature };
  switch (signature[0]) {
    case "[":
      return decodeDetailsSignature(signature);
    case "{":
      return decodeOpaqueSignature(signature);
    default:
      return void 0;
  }
}
__name(decodeThinkingSignature, "decodeThinkingSignature");
function isOpenAICompletionsReasoningField(field) {
  return OPENAI_COMPLETIONS_REASONING_FIELDS.includes(field);
}
__name(isOpenAICompletionsReasoningField, "isOpenAICompletionsReasoningField");
function decodeDetailsSignature(signature) {
  const parsed = parseJson(signature);
  return Array.isArray(parsed) && parsed.length > 0 && parsed.every(isOpenAIReasoningDetail) ? { kind: "details", details: parsed } : void 0;
}
__name(decodeDetailsSignature, "decodeDetailsSignature");
function decodeOpaqueSignature(signature) {
  const parsed = parseJson(signature);
  if (!isJsonObject(parsed))
    return void 0;
  const { field, reasoning_opaque: opaque } = parsed;
  if (typeof opaque !== "string" || opaque.length === 0)
    return void 0;
  if (field === void 0)
    return { kind: "opaque", opaque };
  return typeof field === "string" && isOpenAICompletionsReasoningField(field) ? { kind: "opaque", field, opaque } : void 0;
}
__name(decodeOpaqueSignature, "decodeOpaqueSignature");
function parseJson(text) {
  try {
    return JSON.parse(text);
  } catch {
    return void 0;
  }
}
__name(parseJson, "parseJson");
function isJsonObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isJsonObject, "isJsonObject");
function isOpenAIReasoningDetail(detail) {
  if (!isJsonObject(detail) || !hasValidCommonReasoningDetailFields(detail)) {
    return false;
  }
  switch (detail.type) {
    case "reasoning.summary":
      return typeof detail.summary === "string";
    case "reasoning.encrypted":
      return typeof detail.data === "string";
    case "reasoning.text":
      return typeof detail.text === "string" && (detail.signature === void 0 || detail.signature === null || typeof detail.signature === "string");
    default:
      return false;
  }
}
__name(isOpenAIReasoningDetail, "isOpenAIReasoningDetail");
function hasValidCommonReasoningDetailFields(candidate) {
  return (candidate.id === void 0 || candidate.id === null || typeof candidate.id === "string") && (candidate.format === void 0 || typeof candidate.format === "string") && (candidate.index === void 0 || typeof candidate.index === "number");
}
__name(hasValidCommonReasoningDetailFields, "hasValidCommonReasoningDetailFields");
function decodeLegacyToolCallReasoningDetail(thoughtSignature) {
  if (!thoughtSignature?.startsWith("{"))
    return void 0;
  const parsed = parseJson(thoughtSignature);
  return isOpenAIReasoningDetail(parsed) && parsed.type === "reasoning.encrypted" && typeof parsed.id === "string" && parsed.id.length > 0 && parsed.data.length > 0 ? parsed : void 0;
}
__name(decodeLegacyToolCallReasoningDetail, "decodeLegacyToolCallReasoningDetail");

// pi-dist/pi-ai/api/transform-messages.js
var NON_VISION_USER_IMAGE_PLACEHOLDER = "(image omitted: model does not support images)";
var NON_VISION_TOOL_IMAGE_PLACEHOLDER = "(tool image omitted: model does not support images)";
function replaceImagesWithPlaceholder(content, placeholder) {
  const result = [];
  let previousWasPlaceholder = false;
  for (const block of content) {
    if (block.type === "image") {
      if (!previousWasPlaceholder) {
        result.push({ type: "text", text: placeholder });
      }
      previousWasPlaceholder = true;
      continue;
    }
    result.push(block);
    previousWasPlaceholder = block.text === placeholder;
  }
  return result;
}
__name(replaceImagesWithPlaceholder, "replaceImagesWithPlaceholder");
function downgradeUnsupportedImages(messages, model) {
  if (model.input.includes("image")) {
    return messages;
  }
  return messages.map((msg) => {
    if (msg.role === "user" && Array.isArray(msg.content)) {
      return {
        ...msg,
        content: replaceImagesWithPlaceholder(msg.content, NON_VISION_USER_IMAGE_PLACEHOLDER)
      };
    }
    if (msg.role === "toolResult") {
      return {
        ...msg,
        content: replaceImagesWithPlaceholder(msg.content, NON_VISION_TOOL_IMAGE_PLACEHOLDER)
      };
    }
    return msg;
  });
}
__name(downgradeUnsupportedImages, "downgradeUnsupportedImages");
function transformMessages(messages, model, normalizeToolCallId) {
  const toolCallIdMap = /* @__PURE__ */ new Map();
  const normalizedMessages = messages.map((msg) => msg.content == null ? { ...msg, content: [] } : msg);
  const imageAwareMessages = downgradeUnsupportedImages(normalizedMessages, model);
  const transformed = imageAwareMessages.map((msg) => {
    if (msg.role === "system" || msg.role === "user") {
      return msg;
    }
    if (msg.role === "toolResult") {
      const normalizedId = toolCallIdMap.get(msg.toolCallId);
      if (normalizedId && normalizedId !== msg.toolCallId) {
        return { ...msg, toolCallId: normalizedId };
      }
      return msg;
    }
    if (msg.role === "assistant") {
      const assistantMsg = msg;
      const isSameModel = assistantMsg.provider === model.provider && assistantMsg.api === model.api && assistantMsg.model === model.id;
      const transformedContent = assistantMsg.content.flatMap((block) => {
        if (block.type === "thinking") {
          if (!isSameModel)
            return [];
          if (block.redacted || block.thinkingSignature)
            return block;
          if (!block.thinking || block.thinking.trim() === "")
            return [];
          return block;
        }
        if (block.type === "text") {
          if (isSameModel)
            return block;
          return {
            type: "text",
            text: block.text
          };
        }
        if (block.type === "toolCall") {
          const toolCall = block;
          let normalizedToolCall = toolCall;
          if (!isSameModel && toolCall.thoughtSignature) {
            normalizedToolCall = { ...toolCall };
            delete normalizedToolCall.thoughtSignature;
          }
          if (!isSameModel && normalizeToolCallId) {
            const normalizedId = normalizeToolCallId(toolCall.id, model, assistantMsg);
            if (normalizedId !== toolCall.id) {
              toolCallIdMap.set(toolCall.id, normalizedId);
              normalizedToolCall = { ...normalizedToolCall, id: normalizedId };
            }
          }
          return normalizedToolCall;
        }
        return block;
      });
      return {
        ...assistantMsg,
        content: transformedContent
      };
    }
    return msg;
  });
  const result = [];
  let pendingToolCalls = [];
  let existingToolResultIds = /* @__PURE__ */ new Set();
  const heldSystemMessages = [];
  const closePendingToolCalls = /* @__PURE__ */ __name(() => {
    if (pendingToolCalls.length > 0) {
      for (const tc of pendingToolCalls) {
        if (!existingToolResultIds.has(tc.id)) {
          result.push({
            role: "toolResult",
            toolCallId: tc.id,
            toolName: tc.name,
            content: [{ type: "text", text: "No result provided" }],
            isError: true,
            timestamp: Date.now()
          });
        }
      }
      pendingToolCalls = [];
      existingToolResultIds = /* @__PURE__ */ new Set();
    }
    result.push(...heldSystemMessages);
    heldSystemMessages.length = 0;
  }, "closePendingToolCalls");
  for (let i = 0; i < transformed.length; i++) {
    const msg = transformed[i];
    if (msg.role === "assistant") {
      closePendingToolCalls();
      const assistantMsg = msg;
      if (assistantMsg.stopReason === "error" || assistantMsg.stopReason === "aborted") {
        continue;
      }
      const toolCalls = assistantMsg.content.filter((b) => b.type === "toolCall");
      if (toolCalls.length > 0) {
        pendingToolCalls = toolCalls;
        existingToolResultIds = /* @__PURE__ */ new Set();
      }
      result.push(msg);
    } else if (msg.role === "toolResult") {
      existingToolResultIds.add(msg.toolCallId);
      result.push(msg);
    } else if (msg.role === "system") {
      if (pendingToolCalls.length > 0) {
        heldSystemMessages.push(msg);
      } else {
        result.push(msg);
      }
    } else if (msg.role === "user") {
      closePendingToolCalls();
      result.push(msg);
    } else {
      result.push(msg);
    }
  }
  closePendingToolCalls();
  return result;
}
__name(transformMessages, "transformMessages");

// pi-dist/pi-ai/api/openai-completions.impl.js
function isTextContentBlock(block) {
  return block.type === "text";
}
__name(isTextContentBlock, "isTextContentBlock");
function isThinkingContentBlock(block) {
  return block.type === "thinking";
}
__name(isThinkingContentBlock, "isThinkingContentBlock");
function isToolCallBlock(block) {
  return block.type === "toolCall";
}
__name(isToolCallBlock, "isToolCallBlock");
function isImageContentBlock(block) {
  return block.type === "image";
}
__name(isImageContentBlock, "isImageContentBlock");
function convertMessages(model, context, compat, options) {
  const normalizedContext = resolveTranscript(context, compat.supportsMidConvoSystemMessages);
  const params = [];
  const normalizeToolCallId = /* @__PURE__ */ __name((id) => {
    if (id.includes("|")) {
      const separatorIndex = id.indexOf("|");
      const callId = id.slice(0, separatorIndex).replace(/[^a-zA-Z0-9_-]/g, "_");
      const itemId = id.slice(separatorIndex + 1).replace(/[^a-zA-Z0-9_-]/g, "_");
      const combinedId = itemId.length > 0 ? `${callId}_${itemId}` : callId;
      if (combinedId.length <= 40) {
        return combinedId;
      }
      const hash = shortHash(id).slice(0, 8);
      const prefix = callId.slice(0, Math.max(1, 40 - hash.length - 1));
      return `${prefix}_${hash}`;
    }
    if (model.provider === "openai")
      return id.length > 40 ? id.slice(0, 40) : id;
    return id;
  }, "normalizeToolCallId");
  const transformedMessages = transformMessages(normalizedContext.messages, model, (id) => normalizeToolCallId(id));
  const transcriptTools = resolveTranscriptTools(normalizedContext.messages, compat.supportsMidConvoSystemMessages === true && compat.supportsMidConvoToolAdditions === true ? "additions" : "none");
  const instructionRole = model.reasoning && compat.supportsDeveloperRole ? "developer" : "system";
  let lastRole = null;
  let systemIndex = 0;
  for (let i = 0; i < transformedMessages.length; i++) {
    const msg = transformedMessages[i];
    if (compat.requiresAssistantAfterToolResult && lastRole === "toolResult" && msg.role === "user") {
      params.push({
        role: "assistant",
        content: "I have processed the tool results."
      });
    }
    if (msg.role === "system") {
      const addedTools = systemIndex++ >= transcriptTools.foldedSystemMessages ? msg.toolsAdded ?? [] : [];
      if (addedTools.length > 0) {
        const kimiToolMessage = {
          role: "system",
          tools: convertTools(addedTools, compat)
        };
        params.push(kimiToolMessage);
      }
      const text = i === 0 ? getSystemMessageText(msg) : renderSystemMessageUpdate(msg);
      if (text.length > 0) {
        params.push({ role: instructionRole, content: sanitizeSurrogates(text) });
      }
    } else if (msg.role === "user") {
      if (typeof msg.content === "string") {
        params.push({
          role: "user",
          content: sanitizeSurrogates(msg.content)
        });
      } else {
        const content = msg.content.filter((item) => item.type !== "text" || item.text.length > 0).map((item) => {
          if (item.type === "text") {
            return {
              type: "text",
              text: sanitizeSurrogates(item.text)
            };
          } else {
            return {
              type: "image_url",
              image_url: {
                url: `data:${item.mimeType};base64,${item.data}`
              }
            };
          }
        });
        if (content.length === 0)
          continue;
        params.push({
          role: "user",
          content
        });
      }
    } else if (msg.role === "assistant") {
      const assistantMsg = {
        role: "assistant",
        content: compat.requiresAssistantAfterToolResult || compat.requiresNonNullAssistantContent ? "" : null
      };
      const assistantTextParts = msg.content.filter(isTextContentBlock).filter((block) => block.text.trim().length > 0).map((block) => ({
        type: "text",
        text: sanitizeSurrogates(block.text)
      }));
      const assistantText = assistantTextParts.map((part) => part.text).join("");
      const thinkingBlocks = msg.content.filter(isThinkingContentBlock);
      const toolCalls = msg.content.filter(isToolCallBlock);
      const reasoningReplay = resolveReasoningReplay(thinkingBlocks, toolCalls);
      const nonEmptyThinkingBlocks = thinkingBlocks.filter((block) => block.thinking.trim().length > 0);
      if (nonEmptyThinkingBlocks.length > 0) {
        if (compat.requiresThinkingAsText) {
          const thinkingText = nonEmptyThinkingBlocks.map((block) => sanitizeSurrogates(block.thinking)).join("\n\n");
          assistantMsg.content = [{ type: "text", text: thinkingText }, ...assistantTextParts];
        } else {
          if (assistantText.length > 0) {
            assistantMsg.content = assistantText;
          }
          if (reasoningReplay?.kind !== "details") {
            let field = reasoningReplay?.field;
            if (model.provider === "opencode-go" && field === "reasoning") {
              field = "reasoning_content";
            }
            if (field) {
              assistantMsg[field] = nonEmptyThinkingBlocks.map((block) => block.thinking).join("\n");
            }
          }
        }
      } else if (assistantText.length > 0) {
        assistantMsg.content = assistantText;
      }
      if (toolCalls.length > 0) {
        assistantMsg.tool_calls = toolCalls.map((tc) => {
          const customInputProperty = options?.grammarToolInputProperties?.get(tc.name);
          if (customInputProperty !== void 0) {
            return {
              id: tc.id,
              type: "custom",
              custom: {
                name: tc.name,
                input: sanitizeSurrogates(getGrammarToolInput(tc.name, tc.arguments, customInputProperty))
              }
            };
          }
          return {
            id: tc.id,
            type: "function",
            function: {
              name: tc.name,
              arguments: JSON.stringify(tc.arguments)
            }
          };
        });
      }
      if (reasoningReplay?.kind === "details") {
        assistantMsg.reasoning_details = reasoningReplay.details;
      } else if (reasoningReplay?.kind === "opaque") {
        assistantMsg.reasoning_opaque = reasoningReplay.opaque;
      }
      if (compat.requiresReasoningContentOnAssistantMessages && model.reasoning && assistantMsg.reasoning_content === void 0) {
        assistantMsg.reasoning_content = "";
      }
      const content = assistantMsg.content;
      const hasContent = content !== null && content !== void 0 && (typeof content === "string" ? content.length > 0 : content.length > 0);
      if (!hasContent && !assistantMsg.tool_calls) {
        continue;
      }
      params.push(assistantMsg);
    } else if (msg.role === "toolResult") {
      const imageBlocks = [];
      let j = i;
      for (; j < transformedMessages.length && transformedMessages[j].role === "toolResult"; j++) {
        const toolMsg = transformedMessages[j];
        const textResult = toolMsg.content.filter(isTextContentBlock).map((block) => block.text).join("\n");
        const hasImages = toolMsg.content.some((c) => c.type === "image");
        const hasText = textResult.trim().length > 0;
        const toolResultText = hasText ? textResult : hasImages ? "(see attached image)" : "(no tool output)";
        const toolResultMsg = {
          role: "tool",
          content: sanitizeSurrogates(toolResultText),
          tool_call_id: toolMsg.toolCallId
        };
        if (compat.requiresToolResultName && toolMsg.toolName) {
          toolResultMsg.name = toolMsg.toolName;
        }
        params.push(toolResultMsg);
        if (hasImages && model.input.includes("image")) {
          for (const block of toolMsg.content) {
            if (isImageContentBlock(block)) {
              imageBlocks.push({
                type: "image_url",
                image_url: {
                  url: `data:${block.mimeType};base64,${block.data}`
                }
              });
            }
          }
        }
      }
      i = j - 1;
      if (imageBlocks.length > 0) {
        if (compat.requiresAssistantAfterToolResult) {
          params.push({
            role: "assistant",
            content: "I have processed the tool results."
          });
        }
        params.push({
          role: "user",
          content: [
            {
              type: "text",
              text: "Attached image(s) from tool result:"
            },
            ...imageBlocks
          ]
        });
        lastRole = "user";
      } else {
        lastRole = "toolResult";
      }
      continue;
    }
    lastRole = msg.role;
  }
  return params;
}
__name(convertMessages, "convertMessages");
function resolveReasoningReplay(thinkingBlocks, toolCalls) {
  const signatures = thinkingBlocks.map((block) => decodeThinkingSignature(block.thinkingSignature));
  const details = signatures.find((signature) => signature?.kind === "details");
  if (details)
    return details;
  const legacyDetails = toolCalls.map((toolCall) => decodeLegacyToolCallReasoningDetail(toolCall.thoughtSignature)).filter((detail) => detail !== void 0);
  if (legacyDetails.length > 0)
    return { kind: "details", details: legacyDetails };
  const opaque = signatures.findLast((signature) => signature?.kind === "opaque");
  if (opaque)
    return opaque;
  const firstWithText = signatures[thinkingBlocks.findIndex((block) => block.thinking.trim().length > 0)];
  return firstWithText?.kind === "field" ? firstWithText : void 0;
}
__name(resolveReasoningReplay, "resolveReasoningReplay");
function convertTools(tools, compat) {
  return tools.map((tool) => {
    const grammar = resolveGrammarConstrainedSampling(tool, compat.supportsOpenAIGrammarTools);
    if (grammar) {
      return {
        type: "custom",
        custom: {
          name: tool.name,
          description: tool.description,
          format: {
            type: "grammar",
            grammar: {
              syntax: grammar.format,
              definition: grammar.definition
            }
          }
        }
      };
    }
    const strict = resolveJsonSchemaStrictSampling(tool, compat.supportsStrictMode !== false);
    return {
      type: "function",
      function: {
        name: tool.name,
        description: tool.description,
        parameters: getJsonSchemaToolParameters(tool, strict),
        // Only include strict if provider supports it. Some reject unknown fields.
        ...compat.supportsStrictMode !== false && { strict: strict ?? false }
      }
    };
  });
}
__name(convertTools, "convertTools");

// pi-dist/pi-ai/api/openai-completions.js
var { stream, streamSimple } = bridgeApi("openai-completions");
export {
  convertMessages,
  stream,
  streamSimple
};
