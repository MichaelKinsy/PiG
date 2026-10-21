// Pi's pi-ai/models entry (dist/models.js) is served to extensions as its own virtual module under both package scopes
// (core/extensions/virtual-modules.ts, loader.ts getAliases). It comes from the same bundle as the root and compat entries
// so the subpath shares their AI core instead of loading a second copy (automation/gen/bundle-pi-libraries.mjs).
export * from "./pi-dist/pi-ai/sdk-bundle/models.js";
