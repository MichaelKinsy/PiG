import {
  formatProviderError,
  isRecord,
  normalizeProviderError,
  parseClassifierUsage,
  postClassifierRequest,
  requiredNumber
} from "./chunk-FMAFKFFI.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/system-one-shared.js
function probabilities(label, value, id) {
  if (!isRecord(value))
    throw new Error(`${label} returned invalid probabilities for ${id}`);
  return Object.fromEntries(Object.entries(value).map(([key, probability]) => [
    key,
    requiredNumber(label, probability, `probability for ${id}.${key}`)
  ]));
}
__name(probabilities, "probabilities");
function parseAnswers(label, value, context) {
  if (!isRecord(value))
    throw new Error(`${label} returned an unexpected response`);
  const answers = [];
  for (const [id, question] of Object.entries(context.questions)) {
    const answer = value[id];
    if (!isRecord(answer))
      throw new Error(`${label} did not return an answer for ${id}`);
    if (question.type === "choice") {
      if (answer.type !== "choice" || typeof answer.choice !== "string") {
        throw new Error(`${label} did not return a choice answer for ${id}`);
      }
      answers.push([
        id,
        {
          type: "choice",
          choice: answer.choice,
          probabilities: probabilities(label, answer.probabilities, id),
          confidence: requiredNumber(label, answer.confidence, `confidence for ${id}`)
        }
      ]);
    } else if (question.type === "score") {
      if (answer.type !== "score")
        throw new Error(`${label} did not return a score answer for ${id}`);
      answers.push([
        id,
        {
          type: "score",
          score: requiredNumber(label, answer.score, `score for ${id}`),
          confidence: requiredNumber(label, answer.confidence, `confidence for ${id}`)
        }
      ]);
    } else {
      if (answer.type !== "noul")
        throw new Error(`${label} did not return a bool answer for ${id}`);
      answers.push([
        id,
        {
          type: "bool",
          probability: requiredNumber(label, answer.noul, `probability for ${id}`)
        }
      ]);
    }
  }
  return Object.fromEntries(answers);
}
__name(parseAnswers, "parseAnswers");
function wireRequest(context) {
  return {
    state: context.state,
    questions: Object.fromEntries(Object.entries(context.questions).map(([id, question]) => [
      id,
      question.type === "bool" ? { ...question, type: "noul" } : question
    ]))
  };
}
__name(wireRequest, "wireRequest");
async function classifySystemOne(transport, model, context, options) {
  const output = {
    api: model.api,
    provider: model.provider,
    model: model.id,
    answers: {},
    stopReason: "stop",
    timestamp: Date.now()
  };
  try {
    if (model.api !== transport.api)
      throw new Error(`Unsupported classifier API: ${model.api}`);
    if (context.images?.length)
      throw new Error(`${transport.label} does not support image input`);
    const body = await postClassifierRequest(transport.label, transport.url(model), model, transport.payload(model, wireRequest(context)), options);
    const result = transport.output(body);
    const usage = parseClassifierUsage(result.usage, model);
    if (usage)
      output.usage = usage;
    output.answers = parseAnswers(transport.label, result.answers, context);
    return output;
  } catch (error) {
    output.stopReason = options?.signal?.aborted ? "aborted" : "error";
    output.errorMessage = formatProviderError(normalizeProviderError(error), `${transport.label} error`);
    return output;
  }
}
__name(classifySystemOne, "classifySystemOne");

export {
  classifySystemOne
};
