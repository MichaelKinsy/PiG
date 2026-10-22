import { formatProviderError, normalizeProviderError } from "../utils/error-body.js";
import { isRecord, parseClassifierUsage, postClassifierRequest, requiredNumber } from "./classifier-shared.js";
/** TypeSafe's opt-in header for its experimental image input. */
const IMAGE_OPT_IN_HEADERS = { "TypeSafe-Danger-Zone-Images": "true" };
function probabilities(label, value, id) {
    if (!isRecord(value))
        throw new Error(`${label} returned invalid probabilities for ${id}`);
    return Object.fromEntries(Object.entries(value).map(([key, probability]) => [
        key,
        requiredNumber(label, probability, `probability for ${id}.${key}`),
    ]));
}
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
                    confidence: requiredNumber(label, answer.confidence, `confidence for ${id}`),
                },
            ]);
        }
        else if (question.type === "score") {
            if (answer.type !== "score")
                throw new Error(`${label} did not return a score answer for ${id}`);
            answers.push([
                id,
                {
                    type: "score",
                    score: requiredNumber(label, answer.score, `score for ${id}`),
                    confidence: requiredNumber(label, answer.confidence, `confidence for ${id}`),
                },
            ]);
        }
        else {
            if (answer.type !== "noul")
                throw new Error(`${label} did not return a bool answer for ${id}`);
            answers.push([
                id,
                {
                    type: "bool",
                    probability: requiredNumber(label, answer.noul, `probability for ${id}`),
                },
            ]);
        }
    }
    return Object.fromEntries(answers);
}
const ASSET_FORMATS = { "image/png": "png", "image/jpeg": "jpeg" };
const DATA_URL_TYPES = new Set(["image/png", "image/jpeg", "image/webp"]);
/** Clef accepts at most this many images per request. */
const MAX_DATA_URL_IMAGES = 4;
/** Adds the context images to `request` in the service's form. */
function addImages(label, model, input, images, request) {
    if (input === "images") {
        if (images.length > MAX_DATA_URL_IMAGES) {
            throw new Error(`${label} accepts at most ${MAX_DATA_URL_IMAGES} images for ${model.id}, got ${images.length}`);
        }
        request.images = images.map((image) => {
            if (!DATA_URL_TYPES.has(image.mimeType)) {
                throw new Error(`${label} accepts PNG, JPEG, and WebP images for ${model.id}, got ${image.mimeType}`);
            }
            return `data:${image.mimeType};base64,${image.data}`;
        });
        return;
    }
    if (input === "state-image-urls") {
        // OpenRouter validates the images against the model that serves the request.
        const parts = images.map((image) => ({
            type: "image_url",
            image_url: { url: `data:${image.mimeType};base64,${image.data}` },
        }));
        request.state = [...parts, request.state];
        return;
    }
    const assets = {};
    images.forEach((image, index) => {
        const format = ASSET_FORMATS[image.mimeType];
        if (!format)
            throw new Error(`${label} accepts PNG and JPEG images for ${model.id}, got ${image.mimeType}`);
        assets[`<image-${index + 1}>`] = { type: "image", format, data_base64: image.data };
    });
    request.assets = assets;
    // Every asset must be referenced from the request. The names go before the state, where Clef
    // also places its images.
    request.state = [...Object.keys(assets), request.state];
}
/** Maps public `bool` questions to TypeSafe's wire-level `noul` type. */
function wireRequest(context) {
    return {
        state: context.state,
        questions: Object.fromEntries(Object.entries(context.questions).map(([id, question]) => [
            id,
            question.type === "bool" ? { ...question, type: "noul" } : question,
        ])),
    };
}
/** Runs one System One classification over the given transport. */
export async function classifySystemOne(transport, model, context, options) {
    const output = {
        api: model.api,
        provider: model.provider,
        model: model.id,
        answers: {},
        stopReason: "stop",
        timestamp: Date.now(),
    };
    try {
        if (model.api !== transport.api)
            throw new Error(`Unsupported classifier API: ${model.api}`);
        const request = wireRequest(context);
        let requestOptions = options;
        if (context.images?.length) {
            if (!model.input.includes("image")) {
                throw new Error(`${transport.label} does not support image input for ${model.id}`);
            }
            const detected = transport.detectCompat(model);
            const imageInput = model.compat?.imageInput ?? detected.imageInput;
            addImages(transport.label, model, imageInput, context.images, request);
            if (model.compat?.sendImageOptInHeader ?? detected.sendImageOptInHeader) {
                requestOptions = { ...options, headers: { ...IMAGE_OPT_IN_HEADERS, ...options?.headers } };
            }
        }
        const body = await postClassifierRequest(transport.label, transport.url(model), model, transport.payload(model, request), requestOptions);
        const result = transport.output(body);
        // Set before parsing answers: a request with malformed answers was still billed.
        const usage = parseClassifierUsage(result.usage, model);
        if (usage)
            output.usage = usage;
        output.answers = parseAnswers(transport.label, result.answers, context);
        return output;
    }
    catch (error) {
        output.stopReason = options?.signal?.aborted ? "aborted" : "error";
        output.errorMessage = formatProviderError(normalizeProviderError(error), `${transport.label} error`);
        return output;
    }
}
//# sourceMappingURL=system-one-shared.js.map