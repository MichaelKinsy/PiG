// deno-lint-ignore-file no-explicit-any
import { Settings } from '../settings/index.mjs';
import { Metrics } from './metrics.mjs';
import { Freeze } from './freeze.mjs';
function MergeHidden(left, right) {
    for (const key of Object.keys(right)) {
        Object.defineProperty(left, key, {
            configurable: true,
            writable: true,
            enumerable: false,
            value: right[key]
        });
    }
    return left;
}
function Merge(left, right) {
    return { ...left, ...right };
}
/**
 * Creates an object with hidden, enumerable, and optional property sets. This function
 * ensures types are instantiated according to configuration rules for enumerable and
 * non-enumerable properties.
 */
export function Create(hidden, enumerable, options = {}) {
    Metrics.create += 1;
    const withOptions = Merge(enumerable, options);
    const withHidden = Settings.Get().enumerableKind ? Merge(withOptions, hidden) : MergeHidden(withOptions, hidden);
    return Freeze(withHidden);
}
