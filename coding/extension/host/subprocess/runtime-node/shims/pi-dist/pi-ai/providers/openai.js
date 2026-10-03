import { openAIResponsesApi } from "../api/openai-responses.lazy.js";
import { envApiKeyAuth, lazyOAuth } from "../auth/helpers.js";
import { loadOpenAIChatGPTOAuth } from "../auth/oauth/load.js";
import { createProvider } from "../models.js";
import { OPENAI_MODELS } from "./openai.models.js";
export function openaiProvider() {
    return createProvider({
        id: "openai",
        name: "OpenAI",
        baseUrl: "https://api.openai.com/v1",
        auth: {
            apiKey: envApiKeyAuth("OpenAI API key", ["OPENAI_API_KEY"]),
            oauth: lazyOAuth({
                name: "OpenAI (ChatGPT subscription)",
                isSubscription: true,
                loginLabel: "Sign in with ChatGPT",
                load: loadOpenAIChatGPTOAuth,
            }),
        },
        models: Object.values(OPENAI_MODELS),
        api: openAIResponsesApi(),
    });
}
//# sourceMappingURL=openai.js.map