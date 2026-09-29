import { createHarness, fauxAssistantMessage } from "./pi-session-harness.mjs";
import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const { SettingsManager, InMemorySettingsStorage } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/settings-manager.js")));
const model = { provider: "provider", id: "family/model" };
const defaults = { enabled: true, reserveTokens: 16384, keepRecentTokens: 20000 };
const key = "provider/family/model";
function fromDisk(compaction) {
 const storage = new InMemorySettingsStorage();
 storage.withLock("global", () => JSON.stringify({ compaction }));
 return SettingsManager.fromStorage(storage);
}
function failure(manager, model, expected) {
 assert.throws(() => manager.getCompactionSettings(model), error => {
  assert.equal(error.message, expected);
  console.log("COMPACTION_SETTING " + error.message);
  return true;
 });
}
const invalid = [null, -1, 1.5, "400000", true, {}, [], Number.MAX_SAFE_INTEGER + 1];
const nonFinite = [Number.NaN, Infinity, -Infinity];
for (const field of ["reserveTokens", "keepRecentTokens"]) {
 for (const value of invalid) {
  const manager = fromDisk({ modelOverrides: { [key]: { [field]: value } } });
  failure(manager, model, `Invalid compaction.modelOverrides["${key}"].${field} setting: ${String(value)}. Expected a non-negative safe integer.`);
  assert.deepEqual(manager.getCompactionSettings(), defaults);
  assert.deepEqual(manager.getCompactionSettings({ provider: "other", id: model.id }), defaults);
 }
 for (const value of nonFinite) {
  const manager = SettingsManager.inMemory();
  manager.applyOverrides({ compaction: { modelOverrides: { [key]: { [field]: value } } } });
  failure(manager, model, `Invalid compaction.modelOverrides["${key}"].${field} setting: ${String(value)}. Expected a non-negative safe integer.`);
 }
 for (const value of invalid) {
  const manager = fromDisk({ [field]: value, modelOverrides: { [key]: { [field]: 4096 } } });
  const expected = `Invalid compaction.${field} setting: ${String(value)}. Expected a non-negative safe integer.`;
  failure(manager, undefined, expected);
  failure(manager, model, expected);
 }
 for (const value of nonFinite) {
  const manager = SettingsManager.inMemory();
  manager.applyOverrides({ compaction: { [field]: value } });
  failure(manager, undefined, `Invalid compaction.${field} setting: ${String(value)}. Expected a non-negative safe integer.`);
 }
}
for (const value of [null, false, 42, "invalid", []]) {
 failure(fromDisk({ modelOverrides: { [key]: value } }), model, `Invalid compaction.modelOverrides["${key}"] setting: ${String(value)}. Expected an object.`);
}
const expected = "Invalid compaction.reserveTokens setting: -1. Expected a non-negative safe integer.";
const manual = await createHarness({ settings: { compaction: { reserveTokens: -1, modelOverrides: { "faux/faux-1": { reserveTokens: 4096 } } } } });
try {
 manual.setResponses([fauxAssistantMessage("must not run")]);
 await assert.rejects(manual.session.compact(), { message: expected });
 assert.equal(manual.getPendingResponseCount(), 1);
 assert.equal(manual.eventsOfType("compaction_start").length, 1);
 assert.equal(manual.eventsOfType("compaction_end").length, 1);
 assert.equal(manual.eventsOfType("compaction_end")[0].errorMessage, "Compaction failed: " + expected);
 assert.equal(manual.session.isCompacting, false);
 console.log("COMPACTION_CALLER manual requests=0 start=1 end=1 idle=true");
} finally { manual.cleanup(); }
const active = await createHarness();
try {
 active.setResponses([fauxAssistantMessage("first"), fauxAssistantMessage("must not run")]);
 active.session.subscribe(event => {
  if (event.type === "turn_end" && event.message.stopReason === "stop") active.settingsManager.applyOverrides({ compaction: { reserveTokens: -1 } });
 });
 active.session.agent.followUp({ role: "user", content: "next", timestamp: 1 });
 let error;
 try { await active.session.prompt("start"); } catch (caught) { error = caught; }
 assert.equal(active.getPendingResponseCount(), 1);
 const last = active.sessionManager.getBranch().filter(entry => entry.type === "message").at(-1).message;
 assert.equal(last.errorMessage, expected);
 assert.equal(last.stopReason, "error");
 console.log(`COMPACTION_CALLER next requests=1 persisted=true rejected=${error !== undefined}`);
} finally { active.cleanup(); }
