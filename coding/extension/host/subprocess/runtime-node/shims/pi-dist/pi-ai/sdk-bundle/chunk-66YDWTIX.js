import {
  formatProviderError,
  isRecord,
  normalizeProviderError,
  parseClassifierUsage,
  postClassifierRequest,
  requiredNumber
} from "./chunk-FMAFKFFI.js";
import "./chunk-7YNYAWTY.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/openai-decisions.js
var LABEL = "OpenAI Decisions";
var MAX_IMAGES = 128;
function predicateInstructions(question) {
  const meanings = [
    question.criteria.true ? `True means: ${question.criteria.true}` : "",
    question.criteria.false ? `False means: ${question.criteria.false}` : ""
  ].filter(Boolean);
  return meanings.length > 0 ? `${question.instructions}

${meanings.join("\n")}` : question.instructions;
}
__name(predicateInstructions, "predicateInstructions");
function wireQuestion(name, question) {
  if (question.type === "choice") {
    return {
      type: "choice",
      name,
      instructions: question.instructions,
      choices: Object.entries(question.criteria).map(([value, description]) => description ? { value, description } : { value })
    };
  }
  if (question.type === "score") {
    return {
      type: "score",
      name,
      instructions: question.instructions,
      levels: question.criteria.map((label) => ({ label }))
    };
  }
  return { type: "predicate", name, instructions: predicateInstructions(question) };
}
__name(wireQuestion, "wireQuestion");
function wireInput(context) {
  const state = JSON.stringify(context.state);
  const images = context.images ?? [];
  if (images.length === 0)
    return state;
  if (images.length > MAX_IMAGES) {
    throw new Error(`${LABEL} accepts at most ${MAX_IMAGES} images, got ${images.length}`);
  }
  return [
    {
      role: "user",
      content: [
        { type: "input_text", text: state },
        ...images.map((image) => ({
          type: "input_image",
          image_url: `data:${image.mimeType};base64,${image.data}`
        }))
      ]
    }
  ];
}
__name(wireInput, "wireInput");
function choiceProbabilities(value, id) {
  if (!Array.isArray(value))
    throw new Error(`${LABEL} returned invalid probabilities for ${id}`);
  return Object.fromEntries(value.map((entry) => {
    if (!isRecord(entry) || typeof entry.value !== "string") {
      throw new Error(`${LABEL} returned invalid probabilities for ${id}`);
    }
    return [entry.value, requiredNumber(LABEL, entry.probability, `probability for ${id}.${entry.value}`)];
  }));
}
__name(choiceProbabilities, "choiceProbabilities");
function parseAnswer(id, question, answer) {
  if (answer.type === "refusal")
    throw new Error(`${LABEL} refused to answer ${id}`);
  if (question.type === "choice") {
    if (answer.type !== "choice" || typeof answer.choice !== "string") {
      throw new Error(`${LABEL} did not return a choice answer for ${id}`);
    }
    return {
      type: "choice",
      choice: answer.choice,
      probabilities: choiceProbabilities(answer.probabilities, id),
      confidence: requiredNumber(LABEL, answer.confidence, `confidence for ${id}`)
    };
  }
  if (question.type === "score") {
    if (answer.type !== "score")
      throw new Error(`${LABEL} did not return a score answer for ${id}`);
    return {
      type: "score",
      score: requiredNumber(LABEL, answer.score, `score for ${id}`),
      confidence: requiredNumber(LABEL, answer.confidence, `confidence for ${id}`)
    };
  }
  if (answer.type !== "predicate")
    throw new Error(`${LABEL} did not return a predicate answer for ${id}`);
  return { type: "bool", probability: requiredNumber(LABEL, answer.probability, `probability for ${id}`) };
}
__name(parseAnswer, "parseAnswer");
function parseAnswers(value, context) {
  if (!Array.isArray(value))
    throw new Error(`${LABEL} returned an unexpected response`);
  const byName = /* @__PURE__ */ new Map();
  for (const answer of value) {
    if (isRecord(answer) && typeof answer.name === "string")
      byName.set(answer.name, answer);
  }
  return Object.fromEntries(Object.entries(context.questions).map(([id, question]) => {
    const answer = byName.get(id);
    if (!answer)
      throw new Error(`${LABEL} did not return an answer for ${id}`);
    return [id, parseAnswer(id, question, answer)];
  }));
}
__name(parseAnswers, "parseAnswers");
var NO_RETRY_STATUSES = [504];
function errorMessage(error) {
  if (error.status === 504) {
    return `${LABEL} error (504): the request timed out at the gateway. Very large inputs (above roughly 600K tokens) currently exceed its time limit.`;
  }
  return formatProviderError(normalizeProviderError(error), `${LABEL} error`);
}
__name(errorMessage, "errorMessage");
var classify = /* @__PURE__ */ __name(async (model, context, options) => {
  const output = {
    api: model.api,
    provider: model.provider,
    model: model.id,
    answers: {},
    stopReason: "stop",
    timestamp: Date.now()
  };
  try {
    if (model.api !== "openai-decisions")
      throw new Error(`Unsupported classifier API: ${model.api}`);
    const body = await postClassifierRequest(LABEL, new URL("decisions", `${model.baseUrl.replace(/\/+$/u, "")}/`), model, {
      model: model.id,
      input: wireInput(context),
      questions: Object.entries(context.questions).map(([id, question]) => wireQuestion(id, question))
    }, options, NO_RETRY_STATUSES);
    if (!isRecord(body))
      throw new Error(`${LABEL} returned an unexpected response`);
    const usage = parseClassifierUsage(body.usage, model);
    if (usage)
      output.usage = usage;
    output.answers = parseAnswers(body.answers, context);
    return output;
  } catch (error) {
    output.answers = {};
    output.stopReason = options?.signal?.aborted ? "aborted" : "error";
    output.errorMessage = errorMessage(error);
    return output;
  }
}, "classify");
export {
  classify
};
