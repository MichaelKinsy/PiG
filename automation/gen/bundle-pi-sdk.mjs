// Compile the exact coding-agent graph. External host/SDK namespaces retain their identities; source/asset URLs remain unchanged.
import { bundleRuntimePackage } from "./bundle-pi-package.mjs";
const [shims, output] = process.argv.slice(2);
await bundleRuntimePackage(shims, "pi-coding-agent", { index: "index.js", sdk: "core/sdk.js", tools: "core/tools/index.js" }, { output });
