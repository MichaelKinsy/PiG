import { anthropicMessagesApi } from "../api/anthropic-messages.lazy.js";
import { openAICompletionsApi } from "../api/openai-completions.lazy.js";
import { openrouterImagesApi } from "../api/openrouter-images.lazy.js";
import { typesafeSystemOneApi } from "../api/typesafe-system-one.lazy.js";
import { envApiKeyAuth, lazyOAuth } from "../auth/helpers.js";
import { loadOpenRouterOAuth } from "../auth/oauth/load.js";
import { createProvider } from "../models.js";
import { OPENROUTER_CLASSIFIER_MODELS, OPENROUTER_IMAGE_MODELS, OPENROUTER_MODELS } from "./openrouter.models.js";
export function openrouterProvider() {
    return createProvider({
        id: "openrouter",
        name: "OpenRouter",
        baseUrl: "https://openrouter.ai/api/v1",
        auth: {
            apiKey: envApiKeyAuth("OpenRouter API key", ["OPENROUTER_API_KEY"]),
            oauth: lazyOAuth({
                name: "OpenRouter OAuth",
                loginLabel: "Sign in with OpenRouter",
                load: loadOpenRouterOAuth,
            }),
        },
        models: [
            ...Object.values(OPENROUTER_MODELS),
            ...Object.values(OPENROUTER_IMAGE_MODELS),
            ...Object.values(OPENROUTER_CLASSIFIER_MODELS),
        ],
        api: {
            "anthropic-messages": anthropicMessagesApi(),
            "openai-completions": openAICompletionsApi(),
        },
        images: { "openrouter-images": openrouterImagesApi() },
        // OpenRouter serves TypeSafe's System One protocol at /api/v1/systemone.
        classifiers: { "typesafe-system-one": typesafeSystemOneApi() },
    });
}
//# sourceMappingURL=openrouter.js.map