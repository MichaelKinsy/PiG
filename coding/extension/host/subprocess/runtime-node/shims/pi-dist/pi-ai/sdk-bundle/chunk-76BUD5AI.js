import {
  postClassifierRequest,
  requiredNumber
} from "./chunk-IXIM6JAH.js";
import {
  isRecord,
  parsePiUsage
} from "./chunk-PWSD6EHG.js";
import {
  formatProviderError,
  normalizeProviderError
} from "./chunk-HFMBG2Z5.js";
import "./chunk-UVW3US7A.js";
import "./chunk-5FR3CSGH.js";
import "./chunk-5ASU6EDP.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/pi-decision.js
var LABEL = "Pi Decision";
function requiredUnitInterval(value, field) {
  const num = requiredNumber(LABEL, value, field);
  if (num < 0 || num > 1) {
    throw new Error(`${LABEL} returned out-of-range ${field}: ${num}`);
  }
  return num;
}
__name(requiredUnitInterval, "requiredUnitInterval");
function parseProbabilities(value, id, criteria) {
  if (!isRecord(value))
    throw new Error(`${LABEL} returned invalid probabilities for ${id}`);
  return Object.fromEntries(Object.entries(value).map(([choice, prob]) => {
    if (!Object.hasOwn(criteria, choice)) {
      throw new Error(`${LABEL} returned unknown choice "${choice}" in probabilities for ${id}`);
    }
    return [choice, requiredUnitInterval(prob, `probability for ${id}.${choice}`)];
  }));
}
__name(parseProbabilities, "parseProbabilities");
function parseAnswer(id, question, answer) {
  if (!isRecord(answer))
    throw new Error(`${LABEL} returned an invalid answer for ${id}`);
  if (question.type === "choice") {
    if (answer.type !== "choice" || typeof answer.choice !== "string") {
      throw new Error(`${LABEL} did not return a choice answer for ${id}`);
    }
    if (!Object.hasOwn(question.criteria, answer.choice)) {
      throw new Error(`${LABEL} returned unknown choice "${answer.choice}" for ${id}`);
    }
    return {
      type: "choice",
      choice: answer.choice,
      probabilities: parseProbabilities(answer.probabilities, id, question.criteria),
      confidence: requiredUnitInterval(answer.confidence, `confidence for ${id}`)
    };
  }
  if (question.type === "score") {
    if (answer.type !== "score")
      throw new Error(`${LABEL} did not return a score answer for ${id}`);
    const score = requiredNumber(LABEL, answer.score, `score for ${id}`);
    const maxScore = Math.max(0, question.criteria.length - 1);
    if (score < 0 || score > maxScore) {
      throw new Error(`${LABEL} returned out-of-range score for ${id}: ${score}`);
    }
    return {
      type: "score",
      score,
      confidence: requiredUnitInterval(answer.confidence, `confidence for ${id}`)
    };
  }
  if (answer.type !== "bool")
    throw new Error(`${LABEL} did not return a bool answer for ${id}`);
  return { type: "bool", probability: requiredUnitInterval(answer.probability, `probability for ${id}`) };
}
__name(parseAnswer, "parseAnswer");
function parseAnswers(value, context) {
  if (!isRecord(value))
    throw new Error(`${LABEL} returned an unexpected response`);
  return Object.fromEntries(Object.entries(context.questions).map(([id, question]) => {
    if (!Object.hasOwn(value, id))
      throw new Error(`${LABEL} did not return an answer for ${id}`);
    return [id, parseAnswer(id, question, value[id])];
  }));
}
__name(parseAnswers, "parseAnswers");
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
    if (model.api !== "pi-decision")
      throw new Error(`Unsupported classifier API: ${model.api}`);
    const images = context.images ?? [];
    if (images.length > 0 && !model.input.includes("image")) {
      throw new Error(`${LABEL} model ${model.id} does not support image input`);
    }
    const url = new URL("decision", `${model.baseUrl.replace(/\/+$/u, "")}/`);
    const requestBody = {
      model: model.id,
      context: {
        state: context.state,
        ...images.length > 0 ? { images } : {},
        questions: context.questions
      },
      ...options?.sessionId !== void 0 ? { options: { sessionId: options.sessionId } } : {}
    };
    const body = await postClassifierRequest(LABEL, url, model, requestBody, options);
    if (!isRecord(body))
      throw new Error(`${LABEL} returned an unexpected response`);
    if (typeof body.responseId === "string" && body.responseId)
      output.responseId = body.responseId;
    const usage = parsePiUsage(body.usage, model);
    if (usage)
      output.usage = usage;
    output.answers = parseAnswers(body.answers, context);
    return output;
  } catch (error) {
    output.answers = {};
    output.stopReason = options?.signal?.aborted ? "aborted" : "error";
    output.errorMessage = formatProviderError(normalizeProviderError(error), `${LABEL} error`);
    return output;
  }
}, "classify");
export {
  classify
};
