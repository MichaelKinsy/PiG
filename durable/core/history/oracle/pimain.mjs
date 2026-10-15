// Resolve and load hook: run pi-durable main (da866ada) TypeScript sources directly under Node 24 type stripping.
// PI_MAIN overrides the checkout path; the default is the conformance gate's cache (make durable-contract-setup). The load hook exposes the module-private helpers of harness/compaction.ts
// (summaryText, summaryFailure, summaryPrompt and the prompt constants) so the oracle can call them.
import { register } from "node:module";

const root = process.env.PI_MAIN ?? new URL("../../../contract/.cache/pi-main", import.meta.url).pathname;
register(
	"data:text/javascript," +
		encodeURIComponent(`
import {existsSync} from "node:fs";
const R=${JSON.stringify(root + "/packages/")};
const map={"pi-ai":"ai","chord":"chord","pi-agent-core":"agent","pi-durable":"durable","pi-env":"env","pi-client":"client","pi-codemode":"codemode"};
export async function resolve(spec, ctx, next){
  const m=/^@earendil-works\\/([^/]+)(?:\\/(.*))?$/.exec(spec);
  if(m && map[m[1]]){
    const sub=m[2]; const base=R+map[m[1]]+"/src/";
    let f = sub? base+sub+".ts" : base+"index.ts";
    if(sub && !existsSync(f)) f = base+sub+"/index.ts";
    return next("file://"+f, ctx);
  }
  return next(spec, ctx);
}
export async function load(url, ctx, next){
  const r = await next(url, ctx);
  if (url.endsWith("/harness/compaction.ts")) {
    return { ...r, source: Buffer.from(r.source.toString() + "\\nexport { summaryText, summaryFailure, summaryPrompt, SUMMARY_PREFIX, SUMMARY_SUFFIX, SUMMARIZATION_SYSTEM_PROMPT, SUMMARIZATION_PROMPT, placeSummary, TOOL_RESULT_MAX_CHARS };\\n") };
  }
  return r;
}`),
);
export const PI = root;
