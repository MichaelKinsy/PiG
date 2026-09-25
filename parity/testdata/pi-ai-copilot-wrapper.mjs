#!/usr/bin/env node
import { access, readdir } from "node:fs/promises";
import { pathToFileURL } from "url";
import path from "path";

function versionCmp(a, b) {
  const ap = a.split(".").map((n) => Number(n));
  const bp = b.split(".").map((n) => Number(n));
  for (let i = 0; i < Math.max(ap.length, bp.length); i += 1) {
    const d = (ap[i] ?? 0) - (bp[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}

async function resolveCliPath() {
  const packageRoot = process.env.PI_PACKAGE_ROOT;
  const tried = [];
  if (packageRoot) {
    const candidates = [
      path.join(packageRoot, "node_modules/@earendil-works/pi-ai/dist/cli.js"),
      path.join(packageRoot, "../pi-ai/dist/cli.js"),
    ];
    for (const candidate of candidates) {
      tried.push(candidate);
      try {
        await access(candidate);
        return candidate;
      } catch {}
    }
  }

  const home = process.env.HOME;
  const installRoots = [
    {
      root: path.join(home, ".local/share/mise/installs/npm-earendil-works-pi-coding-agent"),
      cliParts: ["lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/cli.js"],
    },
    {
      root: path.join(home, ".local/share/mise/installs/npm-mariozechner-pi-coding-agent"),
      cliParts: ["lib/node_modules/@mariozechner/pi-coding-agent/node_modules/@mariozechner/pi-ai/dist/cli.js"],
    },
  ];
  for (const { root, cliParts } of installRoots) {
    let entries = [];
    try {
      entries = await readdir(root, { withFileTypes: true });
    } catch {
      continue;
    }
    const versions = entries
      .filter((e) => e.isDirectory() && /^\d+\.\d+\.\d+$/.test(e.name))
      .map((e) => e.name)
      .sort(versionCmp)
      .reverse();
    // The parity oracle is pinned to coding.UpstreamVersion (binaries.go
    // enforces an exact match for the direct pi path). When the runner
    // passes that pin via PIG_PARITY_PI_VERSION, require it exactly so a
    // newer pi-ai installed during a bump cannot silently become the
    // oracle. Unpinned, fall back to highest-installed.
    const pinned = process.env.PIG_PARITY_PI_VERSION;
    const selectable = pinned ? versions.filter((v) => v === pinned) : versions;
    for (const version of selectable) {
      for (const rel of cliParts) {
        const candidate = path.join(root, version, rel);
        tried.push(candidate);
        try {
          await access(candidate);
          return candidate;
        } catch {}
      }
    }
  }
  throw new Error(`could not locate pi-ai cli wrapper target; tried:\n${tried.join("\n")}`);
}

const cliPath = await resolveCliPath();

let polls = 0;
globalThis.fetch = async (input, init) => {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url);
  const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  if (url.hostname === "github.com" && url.pathname === "/login/device/code") {
    return json(200, { device_code: "device-123", user_code: "ABCD-EFGH", verification_uri: "https://github.com/login/device", interval: 0, expires_in: 60 });
  }
  if (url.hostname === "github.com" && url.pathname === "/login/oauth/access_token") {
    polls += 1;
    if (polls === 1) return json(200, { error: "authorization_pending" });
    return json(200, { access_token: "ghu_refresh" });
  }
  if (url.hostname === "api.github.com" && url.pathname === "/copilot_internal/v2/token") {
    return json(200, { token: "tid=x;proxy-ep=proxy.individual.githubcopilot.com;other=y", expires_at: 4102444800 });
  }
  if (url.hostname === "api.individual.githubcopilot.com" && url.pathname === "/models") {
    return json(200, { data: [{ id: "gpt-4o", model_picker_enabled: true, policy: { state: "enabled" }, capabilities: { supports: { tool_calls: true } } }] });
  }
  if (url.hostname === "api.individual.githubcopilot.com" && url.pathname.startsWith("/models/") && url.pathname.endsWith("/policy")) {
    return json(200, { ok: true });
  }
  throw new Error(`unexpected oauth request: ${init?.method ?? "GET"} ${url.toString()}`);
};

await import(pathToFileURL(cliPath).href);
