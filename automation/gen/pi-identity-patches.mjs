// PiG's product identity for the Pi JavaScript the Node extension runtime vendors (D26).
//
// A Node extension that calls the Pi SDK or pi-ai runs Pi's own code, which names Pi to
// services (attribution headers, user agents, OAuth originator) and reaches Pi's hosted
// endpoints (version check, catalog, install report, installer). Each entry below replaces
// one exact Pi literal, so a Pi release that changes the line fails the vendoring step
// instead of shipping unpatched. The values are internal/coding/pigidentity/identity.json,
// the file the Go host reads, so both hosts send one identity.
//
// Run through vendor-pi-dist.sh before the SDK and library bundles are compiled.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const identity = JSON.parse(readFileSync(new URL("../../internal/coding/pigidentity/identity.json", import.meta.url), "utf8"));
const note = "// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.\n";

export const identityPatches = {
  "pi-coding-agent/core/provider-attribution.js": [
    ['"HTTP-Referer": "https://pi.dev",', `"HTTP-Referer": "${identity.openRouterReferer}",`],
    ['"X-OpenRouter-Title": "pi",', `"X-OpenRouter-Title": "${identity.openRouterTitle}",`],
    ['"X-BILLING-INVOKE-ORIGIN": "Pi",', `"X-BILLING-INVOKE-ORIGIN": "${identity.nvidiaBillingOrigin}",`],
    ['"User-Agent": "pi-coding-agent",', `"User-Agent": "${identity.cloudflareUserAgent}",`],
    ['"x-opencode-client": "pi" }', `"x-opencode-client": "${identity.openCodeClient}" }`],
  ],
  "pi-coding-agent/utils/pi-user-agent.js": [
    ["export function getPiUserAgent(version) {\n    const runtime = process.versions.bun ? `bun/${process.versions.bun}` : `node/${process.version}`;\n    return `pi/${version} (${process.platform}; ${runtime}; ${process.arch})`;\n}",
      `${note}import { pigUserAgent } from "../../../pig-identity.mjs";\nexport function getPiUserAgent(version) {\n    return pigUserAgent("${identity.userAgentProduct}");\n}`],
  ],
  "pi-ai/utils/pi-user-agent.js": [
    ["export function getPiUserAgent() {\n    return nodeOs ? `pi (${nodeOs.platform()} ${nodeOs.release()}; ${nodeOs.arch()})` : \"pi (browser)\";\n}",
      `${note}import { pigUserAgent } from "../../../pig-identity.mjs";\nexport function getPiUserAgent() {\n    return pigUserAgent("${identity.userAgentProduct}");\n}`],
  ],
  "pi-ai/auth/oauth/openai-codex.js": [
    ['async function createAuthorizationFlow(originator = "pi") {', `async function createAuthorizationFlow(originator = "${identity.codexOriginator}") {`],
  ],
  "pi-ai/auth/oauth/xai.js": [
    ['referrer: "pi",', `referrer: "${identity.xaiReferrer}",`],
  ],
  // D64: PiG's hosted endpoints live on its own origin, with Pi's response shapes.
  "pi-coding-agent/core/remote-catalog-provider.js": [
    ['const DEFAULT_CATALOG_BASE_URL = "https://pi.dev";', `const DEFAULT_CATALOG_BASE_URL = "${identity.hostedOrigin}";`],
  ],
  "pi-coding-agent/utils/version-check.js": [
    ['const LATEST_VERSION_URL = "https://pi.dev/api/latest-version";', `const LATEST_VERSION_URL = "${identity.hostedOrigin}/api/latest-version";`],
  ],
  "pi-coding-agent/package-manager-cli.js": [
    ['const DEFAULT_INSTALLER_API_BASE = "https://pi.dev/api/installer/releases";', `const DEFAULT_INSTALLER_API_BASE = "${identity.hostedOrigin}/api/installer/releases";`],
  ],
  "pi-coding-agent/modes/interactive/interactive-mode.js": [
    ["void fetch(`https://pi.dev/api/report-install?version=", `void fetch(\`${identity.hostedOrigin}/api/report-install?version=`],
  ],
  // D62: PiG never uploads bug reports. D64: PiG never sends a Radius token to Radius's share gateway.
  "pi-coding-agent/core/bug-report-upload.js": [
    ["export async function uploadBugReport(bundle, options = {}) {\n", 'export async function uploadBugReport(bundle, options = {}) {\n    throw new Error("PiG does not upload bug reports (D62): export the report and attach it to a PiG issue instead."); // pig divergence (D26, D62)\n'],
  ],
  "pi-coding-agent/modes/interactive/session-share.js": [
    ["async function tryShareViaRadius(tmpFile, context) {\n", "async function tryShareViaRadius(tmpFile, context) {\n    return false; // pig divergence (D26, D64): PiG never sends a Radius token to Radius's share gateway.\n"],
  ],
};

// patchIdentity returns source, the text of file (relative to shims/pi-dist), with its identity patches applied.
export function patchIdentity(file, source) {
  for (const [before, after] of identityPatches[file] ?? []) {
    if (source.split(before).length !== 2) throw new Error(`Pi identity literal changed in ${file}: ${before}`);
    source = source.replace(before, () => after);
  }
  return source;
}

// applyIdentityPatches rewrites the vendored files under dist (shims/pi-dist).
export function applyIdentityPatches(dist) {
  for (const file of Object.keys(identityPatches)) {
    const path = join(dist, file);
    writeFileSync(path, patchIdentity(file, readFileSync(path, "utf8")));
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) applyIdentityPatches(process.argv[2]);
