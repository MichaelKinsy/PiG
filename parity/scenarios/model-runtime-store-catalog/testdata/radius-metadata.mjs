export default function (pi) {
  pi.registerCommand("radius-metadata", {
    description: "Report restored Radius model metadata.",
    handler: async (_args, ctx) => {
      // ModelRuntime imports legacy catalogs asynchronously during startup; wait
      // for the registry publication rather than racing the command against it.
      let model;
      for (let attempt = 0; attempt < 50; attempt++) {
        model = ctx.modelRegistry.find("radius-dev", "auto");
        if (model?.cost?.tiers?.[0]) break;
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      const tier = model?.cost?.tiers?.[0];
      const report = [
        tier?.inputTokensAbove,
        model?.inputLimits?.maxRequestBytes,
        model?.inputLimits?.images?.resize?.maxWidth,
        model?.promptCache?.short,
        model?.samplingParams?.top_p,
        model?.headers?.["X-Catalog"],
        model?.compat?.supportsStrictMode,
      ].join(":");
      ctx.ui.notify(`radius-metadata:${report}`, "info");
    },
  });
}
