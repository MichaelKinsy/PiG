// Records Pi 0.87.1 OAuth expiry behavior for canonical credential values.
//
// Usage: node test/parity/probes/credential-expiry.mjs <pi-coding-agent package root> > ai/testdata/credential-expiry.json
//
// Each row stores one raw JSON `expires` value (or its absence) and records:
//   - persisted: the provider entry after AuthStorage reads auth.json and rewrites it (JSON.parse then JSON.stringify);
//   - getAuthRefreshes: whether Models.getAuth refreshes it (packages/ai/src/auth/resolve.ts:136-170);
//   - modelRefreshRefreshes: whether Models.refresh refreshes it before refreshModels (packages/ai/src/models.ts:462-478);
//   - minValidityError: whether getAuth with minOAuthValidityMs rejects a refreshed credential carrying the same value.
// Date.now is fixed so the rows are deterministic.
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
if (!root) throw new Error("usage: credential-expiry.mjs <pi-coding-agent package root>");
const ai = await import(pathToFileURL(join(root, "node_modules/@earendil-works/pi-ai/dist/index.js")).href);
const { AuthStorage } = await import(pathToFileURL(join(root, "dist/core/auth-storage.js")).href);

const NOW = 1_700_000_000_000;
Date.now = () => NOW;

const values = [
  undefined,
  "null",
  "0",
  "-0",
  "1.5",
  String(NOW - 1),
  String(NOW),
  String(NOW) + ".25",
  String(NOW + 1),
  String(NOW + 300_000),
  String(NOW + 300_000) + ".5",
  String(NOW + 300_001),
  String(NOW + 3_600_000) + ".75",
  "4102444800000.5",
  "9007199254740993",
  "1152921504606846976",
  "-9007199254740995",
  "1e21",
  "1e400",
  "-1e400",
  "1.7e12",
  "true",
  "false",
  `"${NOW + 3_600_000}"`,
  `" 0x1F "`,
  `"abc"`,
  `""`,
  `[${NOW + 3_600_000}]`,
  "[]",
  "[null]",
  "[1,2]",
  "{}",
];

function provider(refreshValue, counter) {
  return {
    id: "p",
    name: "p",
    auth: {
      oauth: {
        name: "o",
        login: async () => { throw new Error("unused"); },
        refresh: async (credential) => {
          counter.refresh++;
          return refreshValue === undefined ? { ...credential, access: "fresh" } : { ...credential, access: "fresh", expires: refreshValue };
        },
        toAuth: async (credential) => ({ apiKey: credential.access }),
      },
    },
    getModels: () => [],
    refreshModels: async () => { counter.models++; },
    stream: () => { throw new Error("unused"); },
    streamSimple: () => { throw new Error("unused"); },
  };
}

function credential(raw) {
  const text = raw === undefined ? `{"type":"oauth","refresh":"r","access":"a"}` : `{"type":"oauth","refresh":"r","access":"a","expires":${raw}}`;
  return JSON.parse(text);
}

async function persisted(raw) {
  const dir = mkdtempSync(join(tmpdir(), "pi-expiry-"));
  try {
    const path = join(dir, "auth.json");
    const entry = raw === undefined
      ? `{"type":"oauth","refresh":"r","access":"a","meta":{"k":[1,null,""]}}`
      : `{"type":"oauth","refresh":"r","access":"a","expires":${raw},"meta":{"k":[1,null,""]}}`;
    writeFileSync(path, `{"p":${entry}}`);
    const storage = AuthStorage.create(path);
    await storage.modify("p", async (current) => ({ ...current }));
    return JSON.stringify(JSON.parse(readFileSync(path, "utf8")).p);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

async function getAuthRefreshes(raw) {
  const counter = { refresh: 0, models: 0 };
  const store = new ai.InMemoryCredentialStore();
  await store.modify("p", async () => credential(raw));
  const models = ai.createModels({ credentials: store });
  models.setProvider(provider(String(NOW + 3_600_000), counter));
  await models.getAuth("p");
  return counter.refresh > 0;
}

async function modelRefreshRefreshes(raw) {
  const counter = { refresh: 0, models: 0 };
  const store = new ai.InMemoryCredentialStore();
  await store.modify("p", async () => credential(raw));
  const models = ai.createModels({ credentials: store });
  models.setProvider(provider(String(NOW + 3_600_000), counter));
  await models.refresh({ allowNetwork: true });
  return counter.refresh > 0;
}

async function minValidityError(raw) {
  const counter = { refresh: 0, models: 0 };
  const store = new ai.InMemoryCredentialStore();
  await store.modify("p", async () => credential("1"));
  const models = ai.createModels({ credentials: store });
  const value = raw === undefined ? undefined : JSON.parse(raw);
  const p = provider(undefined, counter);
  p.auth.oauth.refresh = async (current) => {
    counter.refresh++;
    const next = { ...current, access: "fresh" };
    if (raw === undefined) delete next.expires;
    else next.expires = value;
    return next;
  };
  models.setProvider(p);
  try {
    await models.getAuth("p", { minOAuthValidityMs: 60_000 });
    return false;
  } catch (error) {
    return /expires too soon/.test(String(error?.message));
  }
}

const rows = [];
for (const raw of values) {
  rows.push({
    raw: raw ?? null,
    absent: raw === undefined,
    persisted: await persisted(raw),
    getAuthRefreshes: await getAuthRefreshes(raw),
    modelRefreshRefreshes: await modelRefreshRefreshes(raw),
    minValidityError: await minValidityError(raw),
  });
}
process.stdout.write(JSON.stringify({ pi: "0.87.1", now: NOW, rows }, null, 2) + "\n");
