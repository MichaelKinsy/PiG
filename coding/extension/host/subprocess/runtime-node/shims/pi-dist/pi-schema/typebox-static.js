// The static types of TypeBox-built schemas, read from the type shapes TypeBox 1.x declares (`'~kind'`, `'~optional'`,
// `'~readonly'`, `'~unsafe'`) without importing TypeBox, so `Static` also types tool arguments of TypeBox schemas.
// Only the common kinds are mapped; every other kind (Enum, Tuple, Intersect, Cyclic, Ref, ...) is `unknown`.
export {};
//# sourceMappingURL=typebox-static.js.map