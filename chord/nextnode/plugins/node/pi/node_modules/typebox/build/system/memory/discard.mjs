// deno-lint-ignore-file no-explicit-any
import { Guard } from '../../guard/index.mjs';
import { Metrics } from './metrics.mjs';
import { Freeze } from './freeze.mjs';
import { Clone } from './clone.mjs';
/** Discards multiple property keys from the given object value */
export function Discard(value, propertyKeys) {
    Metrics.discard += 1;
    const result = {};
    for (const key of Guard.Keys(value)) {
        if (propertyKeys.includes(key))
            continue;
        const descriptor = Object.getOwnPropertyDescriptor(value, key); // safe-name!
        descriptor.value = Clone(descriptor.value);
        Object.defineProperty(result, key, descriptor);
    }
    return Freeze(result);
}
