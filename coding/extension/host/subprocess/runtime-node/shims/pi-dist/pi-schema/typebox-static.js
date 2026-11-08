// The static types of TypeBox-built schemas, read from the type shapes TypeBox 1.x declares (`'~kind'`, `'~optional'`,
// `'~readonly'`, `'~unsafe'`) without importing TypeBox, so `Static` also types tool arguments of TypeBox schemas.
// Only the common kinds are mapped; every other kind (Intersect, Cyclic, Ref, ...) is `unknown`. Tuples are plain
// tuples: an element marked `Type.Optional`, `Type.Rest` or `Type.Readonly` is typed by its schema alone (`[string]`
// where TypeBox gives `[string?]`), unlike TypeBox; tuples of tool and API schemas are fixed pairs, typed exactly.
export {};
//# sourceMappingURL=typebox-static.js.map