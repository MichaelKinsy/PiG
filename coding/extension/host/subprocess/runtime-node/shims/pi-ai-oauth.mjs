// Upstream packages/ai/src/oauth.ts is a type-only entry point: it re-exports
// the extension OAuth declarations and no runtime values. After type stripping
// an extension's import of it becomes a bare side-effect import, which must
// still resolve.
export {};
