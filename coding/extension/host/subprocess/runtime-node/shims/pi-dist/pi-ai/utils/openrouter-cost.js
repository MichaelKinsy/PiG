function isValidCost(value) {
    return typeof value === "number" && Number.isFinite(value) && value >= 0;
}
export function isOpenRouterModel(model) {
    return model.provider === "openrouter" || model.baseUrl.includes("openrouter.ai");
}
/** Replace the catalog total with OpenRouter's billed cost when the response contains complete, valid accounting. */
export function applyOpenRouterReportedCost(usage, reported) {
    if (!isValidCost(reported.cost))
        return;
    if (reported.is_byok === false) {
        usage.cost.total = reported.cost;
        usage.cost.totalFromProvider = true;
        return;
    }
    if (reported.is_byok === true) {
        const details = reported.cost_details;
        if (!details)
            return;
        let upstreamCost = details.upstream_inference_cost;
        if (upstreamCost === null) {
            if (!isValidCost(details.upstream_inference_prompt_cost) ||
                !isValidCost(details.upstream_inference_completions_cost)) {
                return;
            }
            upstreamCost = details.upstream_inference_prompt_cost + details.upstream_inference_completions_cost;
        }
        if (!isValidCost(upstreamCost))
            return;
        usage.cost.total = reported.cost + upstreamCost;
        usage.cost.totalFromProvider = true;
    }
}
//# sourceMappingURL=openrouter-cost.js.map