export const piDecisionApi = () => ({
    classify: async (model, context, options) => (await import("./pi-decision.js")).classify(model, context, options),
});
//# sourceMappingURL=pi-decision.lazy.js.map