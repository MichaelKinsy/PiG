import { Key } from "../../../pi-tui.mjs";
import { Type } from "../../../typebox.mjs";
const modifiers = ["ctrl", "shift", "alt", "super"];
const baseKeyPattern = `(?:[a-z0-9]|${Object.values(Key)
    .flatMap((value) => (typeof value === "string" ? [value.replace(/[\\^$.*+?()[\]{}|]/g, "\\$&")] : []))
    .join("|")})`;
const duplicateModifierPattern = modifiers.map((modifier) => `${modifier}\\+.*${modifier}\\+`).join("|");
export const KeyIdSchema = Type.Unsafe(Type.String({
    pattern: `^(?!.*(?:${duplicateModifierPattern}))(?:(?:${modifiers.join("|")})\\+){0,4}${baseKeyPattern}$`,
    description: "Key identifier with optional ctrl, shift, alt, or super modifiers.",
}));
export function keybindingValueSchema(options) {
    return Type.Union([KeyIdSchema, Type.Array(KeyIdSchema)], options);
}
export const KeybindingValueSchema = keybindingValueSchema();
//# sourceMappingURL=key-id-schema.js.map