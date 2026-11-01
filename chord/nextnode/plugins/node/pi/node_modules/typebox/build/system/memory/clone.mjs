// deno-fmt-ignore-file
import { Guard, GlobalsGuard } from '../../guard/index.mjs';
import { Metrics } from './metrics.mjs';
// ------------------------------------------------------------------
// ClassInstance
//
// TypeBox does not clone arbitrary class instances. Class instances
// cannot be safely cloned without potentially breaking private
// members of the instance.
//
// ------------------------------------------------------------------
function FromClassInstance(value) {
    return value; // atomic
}
// ------------------------------------------------------------------
// SchemaObject
//
// Schema objects have non-enumerable properties that MUST be preserved 
// on Clone. The following is the optimal path these objects.
// ------------------------------------------------------------------
function IsSchemaObject(value) {
    return (Guard.HasPropertyKey(value, '~kind') ||
        Guard.HasPropertyKey(value, '~unsafe'));
}
function FromSchemaObject(value) {
    const result = {};
    for (const key of Guard.Keys(value)) {
        if (Guard.IsUnsafePropertyKey(key))
            continue; // (ignore: prototype-pollution)
        const descriptor = Object.getOwnPropertyDescriptor(value, key); // safe-name!
        descriptor.value = FromValue(descriptor.value);
        if (Guard.IsEqual(descriptor.enumerable, true)) {
            result[key] = descriptor.value;
        }
        else {
            Object.defineProperty(result, key, descriptor);
        }
    }
    return result;
}
// ------------------------------------------------------------------
// PlainObject
// ------------------------------------------------------------------
function FromPlainObject(value) {
    const result = {};
    for (const key of Guard.Keys(value)) {
        if (Guard.IsUnsafePropertyKey(key))
            continue; // (ignore: prototype-pollution)
        result[key] = FromValue(value[key]);
    }
    for (const key of Guard.Symbols(value)) {
        result[key] = FromValue(value[key]);
    }
    return result;
}
// ------------------------------------------------------------------
// Object
// ------------------------------------------------------------------
function FromObject(value) {
    return (Guard.IsClassInstance(value) ? FromClassInstance(value) :
        IsSchemaObject(value) ? FromSchemaObject(value) :
            FromPlainObject(value));
}
// ------------------------------------------------------------------
// Array
// ------------------------------------------------------------------
function FromArray(value) {
    return value.map((element) => FromValue(element));
}
// ------------------------------------------------------------------
// TypeArray
// ------------------------------------------------------------------
function FromTypedArray(value) {
    return value.slice();
}
// ------------------------------------------------------------------
// RegExp
// ------------------------------------------------------------------
function FromRegExp(value) {
    return new RegExp(value.source, value.flags);
}
// ------------------------------------------------------------------
// Map
// ------------------------------------------------------------------
function FromMap(value) {
    return new Map(FromValue([...value.entries()]));
}
// ------------------------------------------------------------------
// Set
// ------------------------------------------------------------------
function FromSet(value) {
    return new Set(FromValue([...value.values()]));
}
function FromValue(value) {
    return (GlobalsGuard.IsTypeArray(value) ? FromTypedArray(value) :
        GlobalsGuard.IsRegExp(value) ? FromRegExp(value) :
            GlobalsGuard.IsMap(value) ? FromMap(value) :
                GlobalsGuard.IsSet(value) ? FromSet(value) :
                    Guard.IsArray(value) ? FromArray(value) :
                        Guard.IsObject(value) ? FromObject(value) :
                            value);
}
// ------------------------------------------------------------------
// Clone
// ------------------------------------------------------------------
/**
 * Returns a Clone of the given value. This function is similar to structuredClone()
 * but also supports deep cloning instances of Map, Set and TypeArray.
 */
export function Clone(value) {
    Metrics.clone += 1;
    return FromValue(value);
}
