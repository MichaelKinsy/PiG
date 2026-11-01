// deno-lint-ignore-file no-explicit-any
import { Settings } from '../settings/index.mjs';
/** Conditionally freezes the value if `immutableTypes` is true, otherwise no action. */
export function Freeze(value) {
    return Settings.Get().immutableTypes ? Object.freeze(value) : value;
}
