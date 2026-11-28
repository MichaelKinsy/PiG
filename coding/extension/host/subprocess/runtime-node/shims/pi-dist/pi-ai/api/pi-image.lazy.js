export const piImageApi = () => ({
    generateImages: async (model, context, options) => (await import("./pi-image.js")).generateImages(model, context, options),
});
//# sourceMappingURL=pi-image.lazy.js.map