// deno-fmt-ignore-file
import * as Schema from '../types/index.mjs';
import { Unique } from './_unique.mjs';
import { Guard as G, EmitGuard as E } from '../../guard/index.mjs';
import { BuildSchema, CheckSchema } from './schema.mjs';
// ------------------------------------------------------------------
// Valid
// ------------------------------------------------------------------
function IsValid(schema) {
    return Schema.IsContains(schema);
}
// ------------------------------------------------------------------
// Build
// ------------------------------------------------------------------
function BuildMinContainsStandard(stack, context, schema, value) {
    const [item, index] = [Unique(), Unique()];
    const count = E.Counted(value, [item, index], E.And(BuildSchema(stack, context, schema.contains, item), context.AddIndex(index)));
    return E.IsGreaterEqualThan(count, E.Constant(schema.minContains));
}
function BuildMinContainsFast(stack, context, schema, value) {
    const [item] = [Unique()];
    const count = E.Counted(value, [item, '_'], BuildSchema(stack, context, schema.contains, item));
    return E.IsGreaterEqualThan(count, E.Constant(schema.minContains));
}
export function BuildMinContains(stack, context, schema, value) {
    if (!IsValid(schema))
        return E.Constant(true);
    return context.UseUnevaluated()
        ? BuildMinContainsStandard(stack, context, schema, value)
        : BuildMinContainsFast(stack, context, schema, value);
}
// ------------------------------------------------------------------
// Check
// ------------------------------------------------------------------
export function CheckMinContains(stack, context, schema, value) {
    if (!IsValid(schema))
        return true;
    const count = G.Counted(value, (item, index) => CheckSchema(stack, context, schema.contains, item) && context.AddIndex(index));
    return G.IsGreaterEqualThan(count, schema.minContains);
}
// ------------------------------------------------------------------
// Error
// ------------------------------------------------------------------
export function ErrorMinContains(stack, context, schemaPath, instancePath, schema, value) {
    return CheckMinContains(stack, context, schema, value) || context.AddError({
        keyword: 'contains',
        schemaPath,
        instancePath,
        params: { minContains: schema.minContains }
    });
}
