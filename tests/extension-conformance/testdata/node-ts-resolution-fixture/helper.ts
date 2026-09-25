// Imported by main.ts through the emitted-JavaScript specifier "./helper.js",
// which is how TypeScript's NodeNext module resolution requires a TypeScript
// source to be referenced.
export function helperLabel(): string {
  return "resolved-ts-source";
}

// A type-only export imported by main.ts WITHOUT the `type` keyword, which
// TypeScript permits. Nothing of it survives type stripping, so the importing
// module must drop the specifier or linking fails.
export interface HelperShape {
  label: string;
}
