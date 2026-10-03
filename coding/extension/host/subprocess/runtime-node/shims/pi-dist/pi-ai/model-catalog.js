function flattenModelCatalog(groups, type) {
    return Object.fromEntries(Object.values(groups)
        .flatMap((models) => Object.values(models))
        .filter((model) => model.type === type)
        .map((model) => [model.id, model]));
}
export function flattenChatModelCatalog(_provider, groups) {
    return flattenModelCatalog(groups, "chat");
}
export function flattenImageModelCatalog(_provider, groups) {
    return flattenModelCatalog(groups, "image");
}
export function flattenClassifierModelCatalog(_provider, groups) {
    return flattenModelCatalog(groups, "classifier");
}
//# sourceMappingURL=model-catalog.js.map