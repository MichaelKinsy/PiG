import { openAICompletionsApi } from "../api/openai-completions.lazy.js";
import { envApiKeyAuth } from "../auth/helpers.js";
import { createProvider } from "../models.js";
import { QWEN_TOKEN_PLAN_INDIVIDUAL_CN_MODELS } from "./qwen-token-plan-individual-cn.models.js";
export function qwenTokenPlanIndividualCnProvider() {
    return createProvider({
        id: "qwen-token-plan-individual-cn",
        name: "Qwen Token Plan Individual CN",
        baseUrl: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
        auth: { apiKey: envApiKeyAuth("Qwen Token Plan Individual CN API key", ["QWEN_TOKEN_PLAN_CN_API_KEY"]) },
        models: Object.values(QWEN_TOKEN_PLAN_INDIVIDUAL_CN_MODELS),
        api: openAICompletionsApi(),
    });
}
//# sourceMappingURL=qwen-token-plan-individual-cn.js.map