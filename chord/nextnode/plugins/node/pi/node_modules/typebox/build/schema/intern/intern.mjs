import { Hashing, Memory } from '../../system/index.mjs';
import { Guard } from '../../guard/index.mjs';
import { Resolve } from '../resolve/index.mjs';
import * as S from '../types/index.mjs';
// ----------------------------------------------------------------
// UnsupportedKeyword
// ----------------------------------------------------------------
function UnsupportedKeyword(keyword) {
    throw Error(`UnsupportedKeyword '${keyword}'`);
}
// ----------------------------------------------------------------
// UnresolvableRef
// ----------------------------------------------------------------
function UnresolvableRef(ref) {
    throw Error(`UnresolvableRef '${ref}'`);
}
// ----------------------------------------------------------------
// HashKey
// ----------------------------------------------------------------
function HashKey(schema) {
    return `x-${Hashing.Hash(schema)}`;
}
// ----------------------------------------------------------------
// AdditionalItems
// ----------------------------------------------------------------
function FromAdditionalItems(context, schema) {
    return FromSchema(context, schema.additionalItems);
}
// ----------------------------------------------------------------
// AdditionalProperties
// ----------------------------------------------------------------
function FromAdditionalProperties(context, schema) {
    return FromSchema(context, schema.additionalProperties);
}
// ----------------------------------------------------------------
// AllOf
// ----------------------------------------------------------------
function FromAllOf(context, schema) {
    return schema.allOf.map((inner) => FromSchema(context, inner));
}
// ----------------------------------------------------------------
// AnyOf
// ----------------------------------------------------------------
function FromAnyOf(context, schema) {
    return schema.anyOf.map((inner) => FromSchema(context, inner));
}
// ----------------------------------------------------------------
// Contains
// ----------------------------------------------------------------
function FromContains(context, schema) {
    return FromSchema(context, schema.contains);
}
// ----------------------------------------------------------------
// DependentSchemas
// ----------------------------------------------------------------
function FromDependentSchemas(context, schema) {
    return Guard.Keys(schema.dependentSchemas).reduce((result, key) => ({ ...result, [key]: FromSchema(context, schema.dependentSchemas[key]) }), {});
}
// ----------------------------------------------------------------
// Else
// ----------------------------------------------------------------
function FromElse(context, schema) {
    return FromSchema(context, schema.else);
}
// ----------------------------------------------------------------
// If
// ----------------------------------------------------------------
function FromIf(context, schema) {
    return FromSchema(context, schema.if);
}
// ----------------------------------------------------------------
// Items
// ----------------------------------------------------------------
function FromItems(context, schema) {
    return S.IsItemsSized(schema) ? FromItemsSized(context, schema) : FromItemsUnsized(context, schema);
}
// ----------------------------------------------------------------
// ItemsSized
// ----------------------------------------------------------------
function FromItemsSized(context, schema) {
    return schema.items.map((inner) => FromSchema(context, inner));
}
// ----------------------------------------------------------------
// ItemsUnsized
// ----------------------------------------------------------------
function FromItemsUnsized(context, schema) {
    return FromSchema(context, schema.items);
}
// ----------------------------------------------------------------
// Not
// ----------------------------------------------------------------
function FromNot(context, schema) {
    return FromSchema(context, schema.not);
}
// ----------------------------------------------------------------
// OneOf
// ----------------------------------------------------------------
function FromOneOf(context, schema) {
    return schema.oneOf.map((inner) => FromSchema(context, inner));
}
// ----------------------------------------------------------------
// PatternProperties
// ----------------------------------------------------------------
function FromPatternProperties(context, schema) {
    return Guard.Keys(schema.patternProperties).reduce((result, key) => ({ ...result, [key]: FromSchema(context, schema.patternProperties[key]) }), {});
}
// ----------------------------------------------------------------
// PrefixItems
// ----------------------------------------------------------------
function FromPrefixItems(context, schema) {
    return schema.prefixItems.map((inner) => FromSchema(context, inner));
}
// ----------------------------------------------------------------
// Properties
// ----------------------------------------------------------------
function FromProperties(context, schema) {
    return Guard.Keys(schema.properties).reduce((result, key) => ({ ...result, [key]: FromSchema(context, schema.properties[key]) }), {});
}
// ----------------------------------------------------------------
// PropertyNames
// ----------------------------------------------------------------
function FromPropertyNames(context, schema) {
    return FromSchema(context, schema.propertyNames);
}
// ----------------------------------------------------------------
// Ref
// ----------------------------------------------------------------
function ResolveRef(context, schema, ref) {
    return Resolve.Ref(context, schema, Resolve.DefaultBase, ref) ?? UnresolvableRef(ref);
}
function FromRef(context, schema) {
    // Resolve target
    const target = ResolveRef(context.context, context.schema, schema.$ref);
    // Check if target is resolving, if not, resolve
    const resolving = context.resolving.get(target);
    if (Guard.IsUndefined(resolving))
        return FromSchema(context, target);
    // Target is mid-intern, so this is a cycle (point at its reserved placeholder)
    resolving.used = true;
    return { $ref: `#/$defs/${resolving.key}` };
}
// ----------------------------------------------------------------
// Then
// ----------------------------------------------------------------
function FromThen(context, schema) {
    return FromSchema(context, schema.then);
}
// ----------------------------------------------------------------
// UnevaluatedItems
// ----------------------------------------------------------------
function FromUnevaluatedItems(context, schema) {
    return FromSchema(context, schema.unevaluatedItems);
}
// ----------------------------------------------------------------
// UnevaluatedProperties
// ----------------------------------------------------------------
function FromUnevaluatedProperties(context, schema) {
    return FromSchema(context, schema.unevaluatedProperties);
}
// ----------------------------------------------------------------
// SchemaObject
// ----------------------------------------------------------------
function FromSchemaObject(context, schema) {
    // Reference schemas cannot contain other keywords
    if (S.IsRef(schema))
        return FromRef(context, schema);
    // Check if the schema has already been resolved
    const existing = resolved.get(schema);
    if (!Guard.IsUndefined(existing))
        return existing;
    // These keywords are unsupported
    if (S.IsDynamicRef(schema))
        UnsupportedKeyword('$dynamicRef');
    if (S.IsRecursiveRef(schema))
        UnsupportedKeyword('$recursiveRef');
    // Reserve a placeholder key in case a nested ref cycles back to this schema
    const reservation = { key: `x-ref-${context.resolving.size}`, used: false };
    context.resolving.set(schema, reservation);
    // Intern each subschema
    const remapped = {
        ...(S.IsRefine(schema) ? { ['~refine']: schema['~refine'] } : {}),
        ...(S.IsAdditionalItems(schema) ? { additionalItems: FromAdditionalItems(context, schema) } : {}),
        ...(S.IsAdditionalProperties(schema) ? { additionalProperties: FromAdditionalProperties(context, schema) } : {}),
        ...(S.IsAllOf(schema) ? { allOf: FromAllOf(context, schema) } : {}),
        ...(S.IsAnyOf(schema) ? { anyOf: FromAnyOf(context, schema) } : {}),
        ...(S.IsContains(schema) ? { contains: FromContains(context, schema) } : {}),
        ...(S.IsDependentSchemas(schema) ? { dependentSchemas: FromDependentSchemas(context, schema) } : {}),
        ...(S.IsElse(schema) ? { else: FromElse(context, schema) } : {}),
        ...(S.IsIf(schema) ? { if: FromIf(context, schema) } : {}),
        ...(S.IsItems(schema) ? { items: FromItems(context, schema) } : {}),
        ...(S.IsNot(schema) ? { not: FromNot(context, schema) } : {}),
        ...(S.IsOneOf(schema) ? { oneOf: FromOneOf(context, schema) } : {}),
        ...(S.IsPatternProperties(schema) ? { patternProperties: FromPatternProperties(context, schema) } : {}),
        ...(S.IsPrefixItems(schema) ? { prefixItems: FromPrefixItems(context, schema) } : {}),
        ...(S.IsProperties(schema) ? { properties: FromProperties(context, schema) } : {}),
        ...(S.IsPropertyNames(schema) ? { propertyNames: FromPropertyNames(context, schema) } : {}),
        ...(S.IsThen(schema) ? { then: FromThen(context, schema) } : {}),
        ...(S.IsUnevaluatedItems(schema) ? { unevaluatedItems: FromUnevaluatedItems(context, schema) } : {}),
        ...(S.IsUnevaluatedProperties(schema) ? { unevaluatedProperties: FromUnevaluatedProperties(context, schema) } : {})
    };
    context.resolving.delete(schema);
    // Finalize and register the result
    const interned = Memory.Discard(Memory.Assign(schema, remapped), ['$id']);
    const key = reservation.used ? reservation.key : HashKey(interned);
    registry.set(key, interned);
    // Result
    const result = { $ref: `#/$defs/${key}` };
    resolved.set(schema, result);
    return result;
}
// ----------------------------------------------------------------
// SchemaBoolean
// ----------------------------------------------------------------
function FromSchemaBoolean(_context, schema) {
    // Finalize and register the result
    const key = HashKey(schema);
    registry.set(key, schema);
    // Result
    const result = { $ref: `#/$defs/${key}` };
    resolved.set(schema, result);
    return result;
}
// ----------------------------------------------------------------
// Schema
// ----------------------------------------------------------------
function FromSchema(context, schema) {
    return S.IsSchemaBoolean(schema) ? FromSchemaBoolean(context, schema) : FromSchemaObject(context, schema);
}
// ----------------------------------------------------------------
// BooleanEntry
// ----------------------------------------------------------------
function BooleanEntry(schema) {
    const key = HashKey(schema);
    return { $ref: `#/$defs/${key}`, $defs: { [key]: schema } };
}
// ----------------------------------------------------------------
// Module-level accumulator state
// ----------------------------------------------------------------
const registry = new Map();
const resolved = new Map();
/**
 * [Experimental] Performs a Common Subexpression Elimination (CSE) transform on the given
 * schema. This function restructures the schema such that each distinct sub-schema is stored
 * exactly once in a $defs object and keyed by content hash. This function can be used to
 * both compress and optimize schemas prior to compilation.
 */
export function Intern(schema) {
    registry.clear();
    resolved.clear();
    if (S.IsSchemaBoolean(schema))
        return BooleanEntry(schema);
    const context = S.IsDefs(schema) ? schema.$defs : {};
    const entry = S.IsRef(schema) ? ResolveRef(context, schema, schema.$ref) : schema;
    if (S.IsSchemaBoolean(entry))
        return BooleanEntry(entry);
    const ref_context = { schema, context, resolving: new Map() };
    const result = FromSchema(ref_context, entry);
    return { $ref: result.$ref, $defs: Object.fromEntries(registry) };
}
