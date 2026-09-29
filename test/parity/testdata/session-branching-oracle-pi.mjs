// Probe the three original branching inputs with deterministic provider replies.
// This establishes the pinned implementation's native system entries; it is not a PiG port of the three tests.
import assert from "node:assert/strict";
import {existsSync, mkdtempSync, rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {production, resolvePackage} from "./session-harness-pi.mjs";

const {registerFauxProvider, fauxAssistantMessage} = await import(resolvePackage("@earendil-works/pi-ai/compat"));
const {createAgentSessionRuntime, createAgentSessionServices, createAgentSessionFromServices} = await production("core/agent-session-runtime.js");
const {SessionManager} = await production("core/session-manager.js");
const cases = [
  [90, false, ["Say hello"], 0, ["system"]],
  [110, true, ["Say hi"], 0, ["system"]],
  [131, false, ["Say one", "Say two", "Say three"], 1, ["system", "user", "assistant"]],
];
for (const [site, memory, prompts, index, expectedRoles] of cases) {
  const directory = mkdtempSync(join(tmpdir(), "pi-branching-oracle-"));
  const faux = registerFauxProvider();
  const model = faux.getModel();
  faux.setResponses(prompts.map(() => fauxAssistantMessage("Hello")));
  let runtime;
  try {
    const factory = async ({cwd, sessionManager, sessionStartEvent}) => {
      const services = await createAgentSessionServices({cwd, agentDir: directory,
        resourceLoaderOptions: {noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true}});
      services.modelRuntime.registerProvider(model.provider, {baseUrl: model.baseUrl, apiKey: "faux-key", api: faux.api,
        models: faux.models.map(m => ({id: m.id, name: m.name, api: m.api, reasoning: m.reasoning, input: m.input,
          cost: m.cost, contextWindow: m.contextWindow, maxTokens: m.maxTokens, baseUrl: m.baseUrl}))});
      return {...await createAgentSessionFromServices({services, sessionManager, sessionStartEvent, model,
        tools: ["read", "bash", "edit", "write"]}), services, diagnostics: services.diagnostics};
    };
    runtime = await createAgentSessionRuntime(factory, {cwd: directory, agentDir: directory,
      sessionManager: memory ? SessionManager.inMemory(directory) : SessionManager.create(directory)});
    for (const prompt of prompts) {
      await runtime.session.prompt(prompt);
      await runtime.session.agent.waitForIdle();
    }
    const previous = runtime.session;
    const users = previous.getUserMessagesForForking();
    assert.deepEqual(users.map(message => message.text), prompts);
    const result = await runtime.fork(users[index].entryId);
    assert.equal(result.cancelled, false);
    assert.equal(result.selectedText, prompts[index]);
    assert.notEqual(runtime.session, previous);
    const roles = runtime.session.messages.map(message => message.role);
    assert.deepEqual(roles, expectedRoles);
    const file = runtime.session.sessionFile;
    if (memory) assert.equal(file, undefined);
    else if (index === 0) assert.equal(existsSync(file), false);
    console.log("BRANCHING_ORACLE " + JSON.stringify({site, prompts, selectedText: result.selectedText, roles,
      file: file === undefined ? "undefined" : existsSync(file)}));
  } finally {
    await runtime?.dispose();
    faux.unregister();
    rmSync(directory, {recursive: true, force: true});
  }
}
