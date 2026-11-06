// Prints the pages Pi's MCP sign-in serves (extensions/mcp/oauth.ts renderPage): oauthSuccessHtml with the sign-in message for an ok callback, and oauthErrorHtml(message, details) for any other.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { oauthSuccessHtml, oauthErrorHtml } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/utils/oauth-page.js"));
const render = (page) => (page.ok ? oauthSuccessHtml("Signed in to the MCP server. You may now close this page.") : oauthErrorHtml(page.message, page.details));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map(render)), () => process.exit(0));
