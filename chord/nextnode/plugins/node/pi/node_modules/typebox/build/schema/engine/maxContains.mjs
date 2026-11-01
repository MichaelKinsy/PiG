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
export function BuildMaxContains(stack, context, schema, value) {
    if (!IsValid(schema))
        return E.Constant(true);
    const [item] = [Unique()];
    const count = E.Counted(value, [item, '_'], BuildSchema(stack, context, schema.contains, item));
    return E.IsLessEqualThan(count, E.Constant(schema.maxContains));
}
// ------------------------------------------------------------------
// Check
// ------------------------------------------------------------------
export function CheckMaxContains(stack, context, schema, value) {
    if (!IsValid(schema))
        return true;
    const count = G.Counted(value, (item) => CheckSchema(stack, context, schema.contains, item));
    return G.IsLessEqualThan(count, schema.maxContains);
}
// ------------------------------------------------------------------
// Error
// ------------------------------------------------------------------
export function ErrorMaxContains(stack, context, schemaPath, instancePath, schema, value) {
    const minContains = Schema.IsMinContains(schema) ? schema.minContains : 1;
    return CheckMaxContains(stack, context, schema, value) || context.AddError({
        keyword: 'contains',
        schemaPath,
        instancePath,
        params: { minContains, maxContains: schema.maxContains },
    });
}
