import { existsSync, readFileSync, realpathSync } from "fs";
import { dirname, join } from "path";
import { pathToFileURL } from "url";

const PI_PACKAGE_NAMES = ["@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent"];

const findPiPackageRoot = (): string => {
  let dir = dirname(realpathSync(process.argv[1] ?? process.execPath));
  while (true) {
    const packageJSON = join(dir, "package.json");
    if (existsSync(packageJSON)) {
      try {
        const pkg = JSON.parse(readFileSync(packageJSON, "utf8"));
        if (PI_PACKAGE_NAMES.includes(pkg?.name)) return dir;
      } catch {}
    }
    const parent = dirname(dir);
    if (parent === dir) throw new Error("could not locate pi-coding-agent package root");
    dir = parent;
  }
};

const piRoot = findPiPackageRoot();

// Try earendil-works first, fall back to mariozechner.
const piAIRoot = (() => {
  const earendil = join(piRoot, "node_modules/@earendil-works/pi-ai");
  if (existsSync(earendil)) return earendil;
  return join(piRoot, "node_modules/@mariozechner/pi-ai");
})();

const loadPKCE = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/pkce.js")).href);
const loadOAuthPage = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/oauth-page.js")).href);
const loadCopilotOAuth = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/github-copilot.js")).href);
const loadAnthropicOAuth = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/anthropic.js")).href);
const loadCodexOAuth = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/openai-codex.js")).href);
const loadOpenRouterOAuth = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/openrouter.js")).href);
const loadXaiOAuth = () => import(pathToFileURL(join(piAIRoot, "dist/auth/oauth/xai.js")).href);
const loadCopilotHeaders = () => import(pathToFileURL(join(piAIRoot, "dist/api/github-copilot-headers.js")).href);
const loadHeaders = () => import(pathToFileURL(join(piAIRoot, "dist/utils/headers.js")).href);
const loadEnvApiKeys = () => import(pathToFileURL(join(piAIRoot, "dist/env-api-keys.js")).href);
const loadBuiltinProviders = () => import(pathToFileURL(join(piAIRoot, "dist/providers/all.js")).href);

// PiG's probe prints its OAuth registry ids under these short aliases.
const OAUTH_PROBE_ALIASES: Record<string, string> = { "github-copilot": "copilot", "openai-codex": "codex" };

// Derive every built-in OAuth flow from the pinned package, including the
// default Radius gateway. PiG uses short aliases for Copilot and Codex.
const builtinOAuthProviderIds = async (): Promise<string> => {
  const { builtinProviders } = await loadBuiltinProviders();
  return builtinProviders()
    .filter((provider: any) => provider.auth?.oauth)
    .map((provider: any) => OAUTH_PROBE_ALIASES[provider.id] ?? provider.id)
    .sort()
    .join(",");
};

const samplePngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aK9sAAAAASUVORK5CYII=";

const jsonResponse = (status: number, body: unknown): Response =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

const makeCodexJWT = (accountId: string) => {
  const header = Buffer.from(JSON.stringify({ alg: "none", typ: "JWT" })).toString("base64url");
  const payload = Buffer.from(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: accountId } })).toString("base64url");
  return `${header}.${payload}.sig`;
};

const withMockFetch = async <T>(handler: (url: URL, init?: RequestInit) => Promise<Response> | Response, fn: () => Promise<T>): Promise<T> => {
  const oldFetch = globalThis.fetch;
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url);
    return handler(url, init);
  };
  try {
    return await fn();
  } finally {
    globalThis.fetch = oldFetch;
  }
};

const installLoginDialogFetch = () => {
  if (process.env.PI_PARITY_LOGIN_DIALOG !== "1") return;
  const oldFetch = globalThis.fetch;
  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url);
    if (url.hostname === "github.com" && url.pathname === "/login/device/code") {
      return jsonResponse(200, { device_code: "dialog-device", user_code: "ABCD-EFGH", verification_uri: "https://github.com/login/device", interval: 30, expires_in: 60 });
    }
    if (url.hostname === "github.com" && url.pathname === "/login/oauth/access_token") {
      return jsonResponse(200, { error: "authorization_pending" });
    }
    return oldFetch(input, init);
  };
};

export default function (pi: any) {
  installLoginDialogFetch();
  pi.registerCommand("probe-oauth-shared", {
    description: "Parity harness: shared OAuth registry + PKCE probe",
    handler: async (_args: string, ctx: any) => {
      const pkce = await loadPKCE();
      const providers = await builtinOAuthProviderIds();
      const generated = await pkce.generatePKCE();
      const urlsafe = /^[A-Za-z0-9_-]+$/.test(generated.verifier) && /^[A-Za-z0-9_-]+$/.test(generated.challenge);
      ctx.ui.notify(`oauth-shared:ids=${providers}:custom=probe-custom:api=probe-access:exp=true:pkce=${generated.verifier.length === 43 && generated.challenge.length === 43 && urlsafe}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-callback-page", {
    description: "Parity harness: OAuth callback HTML rendering probe",
    handler: async (_args: string, ctx: any) => {
      const page = await loadOAuthPage();
      const success = page.oauthSuccessHtml("done <ok>");
      const failure = page.oauthErrorHtml("bad <tag>", "line1\nline2 & more");
      ctx.ui.notify(`oauth-page:success=${success.includes("Authentication successful") && success.includes("done &lt;ok&gt;")}:error=${failure.includes("Authentication failed") && failure.includes("bad &lt;tag&gt;")}:escaped=${!failure.includes("<tag>") && failure.includes("&amp; more")}:details=${failure.includes("line1") && failure.includes("line2")}`, "info");
    },
  });

  pi.registerCommand("probe-copilot-headers", {
    description: "Parity harness: Copilot dynamic headers probe",
    handler: async (_args: string, ctx: any) => {
      const [copilot, headersMod] = await Promise.all([loadCopilotHeaders(), loadHeaders()]);
      const messages = [
        { role: "assistant", content: "done" },
        { role: "user", content: [{ type: "text", text: "caption" }, { type: "image", mimeType: "image/png", bytes: samplePngBase64 }] },
      ];
      const headers = copilot.buildCopilotDynamicHeaders({ messages, hasImages: copilot.hasCopilotVisionInput(messages) });
      const record = headersMod.headersToRecord(new Headers(headers));
      const keys = Object.keys(record).sort().join(",");
      ctx.ui.notify(`copilot-headers:init=${record["x-initiator"]}:intent=${record["openai-intent"]}:vision=${record["copilot-vision-request"]}:keys=${keys}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-copilot", {
    description: "Parity harness: GitHub Copilot OAuth probe",
    handler: async (_args: string, ctx: any) => {
      const { githubCopilotOAuth } = await loadCopilotOAuth();
      let polls = 0;
      let policies = 0;
      const cred = await withMockFetch(async (url, init) => {
        if (url.hostname === "github.com" && url.pathname === "/login/device/code") {
          return jsonResponse(200, { device_code: "device-123", user_code: "ABCD-EFGH", verification_uri: "https://github.com/login/device", interval: 0, expires_in: 60 });
        }
        if (url.hostname === "github.com" && url.pathname === "/login/oauth/access_token") {
          polls += 1;
          if (polls === 1) return jsonResponse(200, { error: "authorization_pending" });
          return jsonResponse(200, { access_token: "ghu_refresh" });
        }
        if (url.hostname === "api.github.com" && url.pathname === "/copilot_internal/v2/token") {
          return jsonResponse(200, { token: "tid=x;proxy-ep=proxy.individual.githubcopilot.com;other=y", expires_at: 4102444800 });
        }
        if (url.hostname === "api.individual.githubcopilot.com" && url.pathname === "/models") {
          return jsonResponse(200, { data: [{ id: "gpt-4o", model_picker_enabled: true, policy: { state: "enabled" }, capabilities: { supports: { tool_calls: true } } }] });
        }
        if (url.hostname === "api.individual.githubcopilot.com" && url.pathname.startsWith("/models/") && url.pathname.endsWith("/policy")) {
          policies += 1;
          return jsonResponse(200, { ok: true });
        }
        throw new Error(`unexpected copilot request ${init?.method ?? "GET"} ${url.toString()}`);
      }, async () => githubCopilotOAuth.login({
        signal: new AbortController().signal,
        prompt: async () => "",
        notify: () => {},
      }));
      const auth = await githubCopilotOAuth.toAuth(cred);
      ctx.ui.notify(`copilot-oauth:refresh=${cred.refresh}:base=${auth.baseUrl}:polls=${polls}:policy=${policies > 0}:exp=${cred.expires > Date.now()}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-copilot-env", {
    description: "Parity harness: GitHub Copilot env-fallback probe",
    handler: async (_args: string, ctx: any) => {
      const [copilot, envApi] = await Promise.all([loadCopilotOAuth(), loadEnvApiKeys()]);
      const envToken = "tid=parity;proxy-ep=proxy.individual.githubcopilot.com;exp=4102444800";
      const prev = process.env.COPILOT_GITHUB_TOKEN;
      process.env.COPILOT_GITHUB_TOKEN = envToken;
      try {
        const bearer = envApi.getEnvApiKey("github-copilot");
        const auth = await copilot.githubCopilotOAuth.toAuth({ type: "oauth", refresh: "", access: bearer, expires: 4102444800000 });
        ctx.ui.notify(`copilot-env:match=${bearer === envToken}:base=${auth.baseUrl}`, "info");
      } finally {
        if (prev === undefined) delete process.env.COPILOT_GITHUB_TOKEN;
        else process.env.COPILOT_GITHUB_TOKEN = prev;
      }
    },
  });

  pi.registerCommand("probe-oauth-anthropic", {
    description: "Parity harness: Anthropic OAuth probe",
    handler: async (_args: string, ctx: any) => {
      const { anthropicOAuth } = await loadAnthropicOAuth();
      let body = "";
      const cred = await withMockFetch(async (url, init) => {
        if (url.hostname === "platform.claude.com" && url.pathname === "/v1/oauth/token") {
          body = String(init?.body ?? "");
          return jsonResponse(200, { access_token: "anth-access", refresh_token: "anth-refresh", expires_in: 3600 });
        }
        throw new Error(`unexpected anthropic request ${url.toString()}`);
      }, async () => anthropicOAuth.login({
        signal: new AbortController().signal,
        prompt: async (prompt: any) => prompt.type === "manual_code" ? "anth-code" : "",
        notify: () => {},
      }));
      ctx.ui.notify(`anthropic-oauth:name=${anthropicOAuth.name}:refresh=${cred.refresh}:access=${cred.access}:grant=${body.includes('"grant_type":"authorization_code"')}:pkce=${body.includes('"code_verifier":"')}:exp=${cred.expires > Date.now()}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-google", {
    description: "Parity harness: Google OAuth probe",
    handler: async (_args: string, ctx: any) => {
      const oauth = await loadOAuthIndex();
      let geminiAuth = "";
      let antAuth = "";
      const [gemini, antigravity] = await withMockFetch(async (url, init) => {
        if (url.hostname === "oauth2.googleapis.com" && url.pathname === "/token") {
          const body = String(init?.body ?? "");
          if (body.includes("client_id=681255809395")) {
            return jsonResponse(200, { access_token: "access-gemini", refresh_token: "refresh-gemini", expires_in: 3600 });
          }
          return jsonResponse(200, { access_token: "access-antigravity", refresh_token: "refresh-antigravity", expires_in: 3600 });
        }
        if (url.hostname === "cloudcode-pa.googleapis.com" && url.pathname === "/v1internal:loadCodeAssist") {
          geminiAuth = String((init?.headers as any)?.Authorization ?? (init?.headers as any)?.authorization ?? "");
          if (geminiAuth === "Bearer access-antigravity") return jsonResponse(200, { cloudaicompanionProject: "project-antigravity" });
          return jsonResponse(200, { allowedTiers: [{ id: "free-tier", isDefault: true }] });
        }
        if (url.hostname === "cloudcode-pa.googleapis.com" && url.pathname === "/v1internal:onboardUser") {
          return jsonResponse(200, { name: "operations/onboard", done: true, response: { cloudaicompanionProject: { id: "project-gemini" } } });
        }
        if (url.hostname === "daily-cloudcode-pa.sandbox.googleapis.com" && url.pathname === "/v1internal:loadCodeAssist") {
          antAuth = String((init?.headers as any)?.Authorization ?? (init?.headers as any)?.authorization ?? "");
          return jsonResponse(200, { cloudaicompanionProject: "project-antigravity" });
        }
        if (url.hostname === "www.googleapis.com") {
          return jsonResponse(200, { email: "user@example.com" });
        }
        throw new Error(`unexpected google request ${url.toString()}`);
      }, async () => {
        const g = await oauth.loginGeminiCli(
          () => {},
          () => {},
          async () => "http://localhost:8085/oauth2callback?code=gemini-code",
        );
        const a = await oauth.loginAntigravity(
          () => {},
          () => {},
          async () => "http://localhost:51121/oauth-callback?code=ant-code",
        );
        return [g, a] as const;
      });
      ctx.ui.notify(`google-oauth:gemini=${gemini.refresh}/${gemini.projectId}:ant=${antigravity.refresh}/${antigravity.projectId}:auths=${geminiAuth}|${antAuth}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-codex", {
    description: "Parity harness: OpenAI Codex OAuth probe",
    handler: async (_args: string, ctx: any) => {
      const { openaiCodexOAuth } = await loadCodexOAuth();
      let form = "";
      const cred = await withMockFetch(async (url, init) => {
        if (url.hostname === "auth.openai.com" && url.pathname === "/oauth/token") {
          form = String(init?.body ?? "");
          return jsonResponse(200, { access_token: makeCodexJWT("acct_123"), refresh_token: "codex-refresh", expires_in: 3600 });
        }
        throw new Error(`unexpected codex request ${url.toString()}`);
      }, async () => openaiCodexOAuth.login({
        signal: new AbortController().signal,
        prompt: async (prompt: any) => prompt.type === "select" ? prompt.options[0]?.id : prompt.type === "manual_code" ? "codex-code" : "",
        notify: () => {},
      }));
      const parts = cred.access.split(".");
      const payload = JSON.parse(Buffer.from(parts[1] ?? "", "base64url").toString("utf8"));
      const accountId = payload?.["https://api.openai.com/auth"]?.chatgpt_account_id ?? "";
      ctx.ui.notify(`codex-oauth:name=${openaiCodexOAuth.name}:refresh=${cred.refresh}:account=${accountId}:grant=${form.includes("grant_type=authorization_code")}:pkce=${form.includes("code_verifier=")}:exp=${cred.expires > Date.now()}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-openrouter", {
    description: "Parity harness: OpenRouter OAuth probe",
    handler: async (_args: string, ctx: any) => {
      const { openRouterOAuth } = await loadOpenRouterOAuth();
      let body = "";
      const cred = await withMockFetch(async (url, init) => {
        if (url.hostname === "openrouter.ai" && url.pathname === "/api/v1/auth/keys") {
          body = String(init?.body ?? "");
          return jsonResponse(200, { key: "or-key" });
        }
        throw new Error(`unexpected openrouter request ${url.toString()}`);
      }, async () => openRouterOAuth.login({
        signal: new AbortController().signal,
        prompt: async (prompt: any) => prompt.type === "manual_code" ? "or-code" : "",
        notify: () => {},
      }));
      const refresh = cred.refresh || "none";
      ctx.ui.notify(`openrouter-oauth:access=${cred.access}:refresh=${refresh}:pkce=${body.includes('"code_verifier":"')}:s256=${body.includes('"code_challenge_method":"S256"')}:exp=${cred.expires > Date.now()}`, "info");
    },
  });

  pi.registerCommand("probe-oauth-xai", {
    description: "Parity harness: xAI OAuth probe",
    handler: async (_args: string, ctx: any) => {
      const { xaiOAuth } = await loadXaiOAuth();
      let form = "";
      const cred = await withMockFetch(async (url, init) => {
        if (url.hostname === "auth.x.ai" && url.pathname === "/oauth2/device/code") {
          return jsonResponse(200, { device_code: "dc", user_code: "UCODE", verification_uri: "https://x.ai/device", expires_in: 600, interval: 1 });
        }
        if (url.hostname === "auth.x.ai" && url.pathname === "/oauth2/token") {
          form = String(init?.body ?? "");
          return jsonResponse(200, { access_token: "xai-access", refresh_token: "xai-refresh", expires_in: 3600 });
        }
        throw new Error(`unexpected xai request ${url.toString()}`);
      }, async () => xaiOAuth.login({
        signal: new AbortController().signal,
        prompt: async () => "",
        notify: () => {},
      }));
      ctx.ui.notify(`xai-oauth:access=${cred.access}:refresh=${cred.refresh}:grant=${form.includes("grant_type=urn")}:exp=${cred.expires > Date.now()}`, "info");
    },
  });
}
