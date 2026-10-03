import { execFileSync } from "node:child_process";
import { createServer } from "node:http";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./ai", "-run", "^TestModelsGenerateImagesAuthThroughOpenRouter$", "-count=1", "-v"], { encoding: "utf8", env: { ...process.env, PIG_PARITY_PROBE: "1" } });
  process.stdout.write(output.split("\n").filter(line => line.startsWith("IMAGES_RUNTIME ")).join("\n") + "\n");
} else {
  const root = join(process.env.PI_PACKAGE_ROOT, "node_modules/@earendil-works/pi-ai/dist");
  const { builtinModels } = await import(pathToFileURL(join(root, "providers/all.js")).href);
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
  let wire, seenEnv;
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    wire = {
      authorization: request.headers.authorization,
      body: canonical(JSON.parse(Buffer.concat(chunks))),
      modelHeader: request.headers["x-model"],
      providerHeader: request.headers["x-provider"],
      removedHeader: "x-removed" in request.headers,
      sharedHeader: request.headers["x-shared"],
    };
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify({ id: "image-response", choices: [{ message: { images: [{ image_url: { url: "data:image/png;base64,aGk=" } }] } }] }));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
    const models = builtinModels({ authContext: { env: async () => undefined, fileExists: async () => false } });
    const provider = models.getProvider("openrouter");
    provider.auth.apiKey.resolve = async () => ({ auth: { apiKey: "provider-key", baseUrl: `http://127.0.0.1:${server.address().port}`, headers: { "X-Provider": "provider", "X-Shared": "provider" } }, env: { PROVIDER_ONLY: "provider", SHARED: "provider" } });
    const generate = provider.generateImages;
    provider.generateImages = async (model, request, options) => { seenEnv = options.env; return generate(model, request, options); };
    const model = { type: "image", id: "fixture-image", name: "Fixture image", api: "openrouter-images", provider: "openrouter", baseUrl: "http://[invalid", headers: { "X-Model": "model", "X-Removed": "remove-me" }, input: ["text"], output: ["image"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const result = await models.generateImages(model, { input: [{ type: "text", text: "a red circle" }] }, { apiKey: "request-key", headers: { "X-Shared": "request", "X-Removed": undefined }, env: { REQUEST_ONLY: "request", SHARED: "request" } });
    if (model.baseUrl !== "http://[invalid") throw new Error("caller model was mutated");
    const output = result.output.map(block => ({ type: block.type, data: block.data, mimeType: block.mimeType }));
    console.log("IMAGES_RUNTIME " + JSON.stringify({ env: canonical(seenEnv), output, responseId: result.responseId, stopReason: result.stopReason, wire }));
  } finally { await new Promise(resolve => server.close(resolve)); }
}
