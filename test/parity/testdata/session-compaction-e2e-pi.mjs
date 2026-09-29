import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { createHarness, fauxAssistantMessage } from "./pi-session-harness.mjs";

const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
const { Agent } = await import(import.meta.resolve("@earendil-works/pi-agent-core", pathToFileURL(root + "/package.json").href));
const { streamSimple } = await import(import.meta.resolve("@earendil-works/pi-ai/compat", pathToFileURL(root + "/package.json").href));
const { AgentSession, SessionManager, SettingsManager, createCodingTools } = await import(pathToFileURL(root + "/dist/index.js"));
const h = await createHarness({ models: [{ id: "faux-1", contextWindow: 200000, maxTokens: 8192 }] });
let session;
try {
 // Match agent-session-compaction.test.ts:createSession exactly except the approved faux model/auth substitution.
 const model = h.getModel();
 const agent = new Agent({ getApiKey: () => "faux-key", streamFn: streamSimple, initialState: { model, systemPrompt: "You are a helpful assistant. Be concise.", tools: createCodingTools(process.cwd()) } });
 const sessionManager = SessionManager.create(h.tempDir);
 const settingsManager = SettingsManager.create(h.tempDir, h.tempDir);
 settingsManager.applyOverrides({ compaction: { keepRecentTokens: 1 } });
 session = new AgentSession({ agent, sessionManager, settingsManager, cwd: h.tempDir, modelRuntime: h.session.modelRuntime, resourceLoader: h.session.resourceLoader });
 h.setResponses([fauxAssistantMessage("4"),fauxAssistantMessage("6"),...Array.from({ length: 6 }, () => fauxAssistantMessage("complete summary"))]);
 await session.prompt("What is 2+2? Reply with just the number.");
 await session.agent.waitForIdle();
 await session.prompt("What is 3+3? Reply with just the number.");
 await session.agent.waitForIdle();
 const result = await session.compact();
 assert(result.summary.length > 0);
 assert(result.tokensBefore > 0);
 const roles=session.messages.map(message=>message.role);
 assert.deepEqual(roles,["system","compactionSummary","assistant"]);
 console.log(JSON.stringify({ summary: result.summary, tokensBefore: result.tokensBefore, roles }));
 console.log("COMPACTION_E2E " + JSON.stringify({ roles, summaryPresent: result.summary.length > 0, tokensPositive: result.tokensBefore > 0 }));
} finally { session?.dispose(); h.cleanup(); }
