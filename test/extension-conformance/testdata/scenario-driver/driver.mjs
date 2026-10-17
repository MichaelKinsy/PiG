// The Node scenario driver: it registers, through the Node SDK's public API,
// the extension the scenario Spec describes (sdkscenarios.Spec). The Go,
// Python and Rust drivers read the same file and do the same, so one scenario
// table exercises every SDK. Keep each driver a straight reading of the Spec:
// behavior belongs in the scenario, not here.
import fs from "node:fs";

// A "${name}" in a string is the answer of the prompt kept as name.
function substitute(value, vars) {
  if (typeof value === "string") return value.replace(/\$\{(\w+)\}/g, (_, name) => vars[name] ?? "");
  if (Array.isArray(value)) return value.map(item => substitute(item, vars));
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, substitute(item, vars)]));
  return value;
}

async function runFlow(flow, interaction, options) {
  const vars = {};
  for (const step of flow.steps ?? []) {
    if (step.deviceId) vars[step.as ?? ""] = options?.getDeviceId ? options.getDeviceId() : "<none>";
    else if (step.notify) interaction.notify(substitute(JSON.parse(step.notify), vars));
    else vars[step.as ?? ""] = await interaction.prompt(JSON.parse(step.prompt));
  }
  return substitute(JSON.parse(flow.result), vars);
}

function providerObject(provider) {
  const model = { id: provider.id + "-model", name: provider.name + " model", provider: provider.id, api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4000, maxTokens: 100 };
  const unused = () => { throw new Error("the scenario provider streams nothing"); };
  const resolve = async ({ credential }) => (credential?.key ? { auth: { apiKey: "resolved-" + credential.key }, source: "stored credential" } : undefined);
  const auth = {};
  if (provider.apiKey) {
    auth.apiKey = { name: provider.apiKey.name, resolve };
    if (provider.apiKey.checkSource) auth.apiKey.check = async () => ({ type: "api_key", source: provider.apiKey.checkSource });
    if (provider.apiKey.login) auth.apiKey.login = interaction => runFlow(provider.apiKey.login, interaction);
  }
  if (provider.oauth) {
    const oauth = provider.oauth;
    auth.oauth = {
      name: oauth.name,
      login: (interaction, options) => runFlow(oauth.login, interaction, options),
      refresh: async credential => ({ ...credential, ...(oauth.refresh ? JSON.parse(oauth.refresh) : {}) }),
      toAuth: async credential => ({ apiKey: credential.access }),
    };
    if (oauth.loginLabel) auth.oauth.loginLabel = oauth.loginLabel;
    if (oauth.subscription) auth.oauth.isSubscription = true;
  }
  return { id: provider.id, name: provider.name, getModels: () => [model], stream: unused, streamSimple: unused, auth };
}

const never = () => new Promise(() => {});

// recorded counts the record-counted lines this process wrote.
let recorded = 0;

// mark records that a tool or command started, for the harness to wait on.
function mark(name) {
  fs.writeFileSync(`.started-${name}`, "");
}

function registerTool(pi, tool) {
  pi.registerTool({
    name: tool.name,
    description: tool.description,
    parameters: tool.schema ? JSON.parse(tool.schema) : { type: "object", properties: { text: { type: "string" } } },
    async execute(_id, params, signal) {
      switch (tool.behavior) {
        case "echo":
          return { content: [{ type: "text", text: "echo:" + (params.text ?? "") }] };
        case "fail":
          throw new Error("boom");
        case "crash":
          throw "tool crash";
        case "details":
          return { content: [{ type: "text", text: "with details" }], details: { count: 2, nested: { a: [1, 2] } } };
        case "soft-error":
          return { content: [{ type: "text", text: "soft failure" }], isError: true };
        case "bad-text":
          return { content: [{ type: "text", text: "a\ud83db" }] };
        case "empty":
          return { content: [] };
        case "blocks":
          return { content: [{ type: "text", text: "a" }, { type: "text", text: "b" }] };
        case "cooperative":
          mark(tool.name);
          await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }));
          return { content: [{ type: "text", text: "cooperative stopped" }] };
        case "hang":
          mark(tool.name);
          return never();
      }
      throw new Error("unknown tool behavior " + tool.behavior);
    },
  });
}

function query(pi, ctx, name) {
  switch (name) {
    case "idle": return ctx.isIdle();
    case "pending": return ctx.hasPendingMessages();
    case "system-prompt": return ctx.getSystemPrompt();
    case "thinking": return ctx.thinkingLevel;
    case "all-tools": return pi.getAllTools().map(({ name, description }) => ({ name, description }));
    case "session-header": { const { type, id, cwd } = ctx.sessionManager.getHeader(); return { type, id, cwd }; }
    case "leaf-entry": { const { type, id } = ctx.sessionManager.getLeafEntry(); return { type, id }; }
    case "session-manager-id": return ctx.sessionManager.getSessionId();
  }
  throw new Error("unknown query " + name);
}

function registryQuery(ctx, arg) {
  const [query, provider] = arg.split(":");
  const registry = ctx.modelRegistry;
  switch (query) {
    case "has-auth": return registry.hasConfiguredAuth({ provider, id: "m" });
    case "using-oauth": return registry.isUsingOAuth({ provider, id: "m" });
    case "provider-name": return registry.getProvider(provider)?.name ?? null;
  }
  throw new Error("unknown registry query " + query);
}

function action(ctx, name) {
  switch (name) {
    case "abort": return ctx.abort();
    case "shutdown": return ctx.shutdown();
    case "compact": return ctx.compact({ customInstructions: "be brief" });
  }
  throw new Error("unknown action " + name);
}

const footer = {};

function lateRegister(pi, kind) {
  switch (kind) {
    case "command": return pi.registerCommand("late-command", { description: "Registered late", handler: async (args) => fs.writeFileSync(args, "ran late") });
    case "shortcut": return pi.registerShortcut("ctrl+alt+l", { description: "Registered late", handler: async () => fs.writeFileSync(".shortcut-fired", "late") });
    case "flag": return pi.registerFlag("late-flag", { description: "Registered late", type: "string", default: "late" });
    case "message-renderer": return pi.registerMessageRenderer("late-type", () => undefined);
    case "entry-renderer": return pi.registerEntryRenderer("late-type", () => undefined);
  }
  throw new Error("unknown late registration " + kind);
}

function registerCommand(pi, command) {
  pi.registerCommand(command.name, {
    description: command.description,
    handler: async (path, ctx) => {
      switch (command.op) {
        case "noop":
          return;
        case "error":
          throw new Error("command boom");
        case "crash":
          throw "command crash";
        case "ping":
          fs.writeFileSync(path, "pong");
          return;
        case "echo":
          fs.writeFileSync(".echo-args", path);
          return;
        case "footer-data": {
          // Pi hands the provider to the footer factory as its third argument.
          if (!footer.provider) {
            ctx.ui.setFooter((_tui, _theme, data) => { footer.provider = data; return { render: () => [], invalidate() {} }; });
            const data = footer.provider;
            data.onBranchChange(() => fs.appendFileSync(".branch-changes", String(data.getGitBranch()) + "\n"));
            data.onBranchChange(() => fs.appendFileSync(".branch-changes", "unsubscribed-callback-ran\n"))();
          }
          const data = footer.provider;
          fs.writeFileSync(path, JSON.stringify({ branch: data.getGitBranch(), statuses: Object.fromEntries(data.getExtensionStatuses()), count: data.getAvailableProviderCount() }));
          return;
        }
        case "late-register":
          lateRegister(pi, command.arg);
          fs.writeFileSync(path, "registered");
          return;
        case "registered-provider": {
          const provider = ctx.modelRegistry.getRegisteredNativeProvider(command.arg);
          fs.writeFileSync(path, provider ? provider.name : "none");
          return;
        }
        case "flag":
          fs.writeFileSync(path, JSON.stringify(pi.getFlag(command.arg) ?? null));
          return;
        case "hang":
          mark(command.name);
          return never();
        case "query":
          fs.writeFileSync(path, JSON.stringify(query(pi, ctx, command.arg)));
          return;
        case "signal-wait": {
          mark(command.name);
          const signal = ctx.signal;
          if (!signal.aborted) await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }));
          fs.writeFileSync(path, "signal aborted while running");
          return;
        }
        case "signal":
          fs.writeFileSync(path, ctx.signal === undefined ? "none" : ctx.signal.aborted ? "aborted" : "live");
          return;
        case "registry":
          fs.writeFileSync(path, JSON.stringify(registryQuery(ctx, command.arg)));
          return;
        case "action":
          action(ctx, command.arg);
          fs.writeFileSync(path, "called");
          return;
        case "reload-stale": {
          await ctx.reload();
          let report = "cwd live";
          try {
            ctx.cwd;
          } catch (error) {
            report = `cwd threw: ${error.message}`;
          }
          fs.writeFileSync(path, report);
          return;
        }
      }
      throw new Error("unknown command op " + command.op);
    },
  });
}

// makeDriver builds the extension factory that reads its Spec from file.
export function makeDriver(file) {
  return function (pi) {
    const spec = JSON.parse(fs.readFileSync(file, "utf8"));
    for (const provider of spec.providers ?? []) pi.registerProvider(providerObject(provider));
    for (const tool of spec.tools ?? []) registerTool(pi, tool);
    for (const command of spec.commands ?? []) registerCommand(pi, command);
    for (const shortcut of spec.shortcuts ?? []) {
      pi.registerShortcut(shortcut.key, { description: shortcut.description, handler: async () => fs.writeFileSync(".shortcut-fired", shortcut.key) });
    }
    for (const event of spec.events ?? []) {
      pi.on(event.name, async (data) => {
        switch (event.op) {
          case "set-content":
            return { content: [{ type: "text", text: "patched" }] };
          case "set-error":
            return { isError: true };
          case "see-content":
            return { details: { saw: data.content?.[0]?.text } };
          case "record":
            fs.appendFileSync(".events", `${event.tag}:${JSON.stringify(data)}\n`);
            return;
          case "record-counted":
            fs.appendFileSync(".events", `${event.tag}@${++recorded}:${JSON.stringify(data)}\n`);
            return;
          case "set-prompt":
            return { systemPrompt: "base prompt" };
          case "append-prompt":
            return { systemPrompt: data.systemPrompt + "+" };
          case "note":
            return { message: { customType: "note", content: "hello", display: true } };
          case "see-prompt":
            return { message: { customType: "saw", content: data.prompt, display: false } };
          case "transform":
            return { action: "transform", text: data.text + "!" };
          case "handled":
            return { action: "handled" };
          case "continue":
            return { action: "continue" };
          case "throw":
            throw new Error("event boom");
          case "crash":
            throw "event crash";
          case "block":
            return { block: true, reason: "event blocked" };
        }
      });
    }
    for (const flag of spec.flags ?? []) {
      const options = { description: flag.description, type: flag.type };
      if (flag.default !== undefined) options.default = flag.default;
      pi.registerFlag(flag.name, options);
    }
  };
}

export default makeDriver(".sdk-scenario.json");
