// deno-fmt-ignore-file
import * as Schema from '../types/index.mjs';
import { Unique } from './_unique.mjs';
import { EmitGuard as E, Guard as G } from '../../guard/index.mjs';
import { BuildSchema, CheckSchema } from './schema.mjs';
// ------------------------------------------------------------------
// Invalid
// ------------------------------------------------------------------
function IsValid(schema) {
    return !(Schema.IsMinContains(schema) && G.IsEqual(schema.minContains, 0));
}
// ------------------------------------------------------------------
// Build
// ------------------------------------------------------------------
function BuildContainsStandard(stack, context, schema, value) {
    const [item, index] = [Unique(), Unique()];
    const isLength = E.Not(E.IsEqual(E.Member(value, 'length'), E.Constant(0)));
    const isSome = E.SomeAll(value, [item, index], E.And(BuildSchema(stack, context, schema.contains, item), context.AddIndex(index)));
    return E.And(isLength, isSome);
}
function BuildContainsFast(stack, context, schema, value) {
    const [item] = [Unique()];
    const isLength = E.Not(E.IsEqual(E.Member(value, 'length'), E.Constant(0)));
    const isSome = E.Some(value, [item, '_'], BuildSchema(stack, context, schema.contains, item));
    return E.And(isLength, isSome);
}
export function BuildContains(stack, context, schema, value) {
    if (!IsValid(schema))
        return E.Constant(true);
    return context.UseUnevaluated()
        ? BuildContainsStandard(stack, context, schema, value)
        : BuildContainsFast(stack, context, schema, value);
}
// ------------------------------------------------------------------
// Check
// ------------------------------------------------------------------
export function CheckContains(stack, context, schema, value) {
    if (!IsValid(schema))
        return true;
    return !G.IsEqual(value.length, 0) && G.SomeAll(value, (item, index) => {
        return CheckSchema(stack, context, schema.contains, item) && context.AddIndex(index);
    });
}
// ------------------------------------------------------------------
// Error
// ------------------------------------------------------------------
export function ErrorContains(stack, context, schemaPath, instancePath, schema, value) {
    return CheckContains(stack, context, schema, value) || context.AddError({
        keyword: 'contains',
        schemaPath,
        instancePath,
        params: { minContains: 1 },
    });
}
