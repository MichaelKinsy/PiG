import { anthropicMessagesApi } from "../api/anthropic-messages.lazy.js";
import { googleGenerativeAIApi } from "../api/google-generative-ai.lazy.js";
import { openAICompletionsApi } from "../api/openai-completions.lazy.js";
import { openAIResponsesApi } from "../api/openai-responses.lazy.js";
import { envApiKeyAuth } from "../auth/helpers.js";
import { createProvider } from "../models.js";
import { OPENCODE_MODELS } from "./opencode.models.js";
import { withOpenCodeSessionHeader } from "./opencode-headers.js";
export function opencodeProvider() {
    return createProvider({
        id: "opencode",
        name: "OpenCode Zen",
        auth: { apiKey: envApiKeyAuth("OpenCode API key", ["OPENCODE_API_KEY"]) },
        models: Object.values(OPENCODE_MODELS),
        api: {
            "anthropic-messages": withOpenCodeSessionHeader(anthropicMessagesApi()),
            "google-generative-ai": withOpenCodeSessionHeader(googleGenerativeAIApi()),
            "openai-completions": withOpenCodeSessionHeader(openAICompletionsApi()),
            "openai-responses": withOpenCodeSessionHeader(openAIResponsesApi()),
        },
    });
}
//# sourceMappingURL=opencode.js.map