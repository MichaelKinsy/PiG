// deno-fmt-ignore-file
import { CheckContext, ErrorContext } from './_context.mjs';
import { Reducer } from './_reducer.mjs';
import { EmitGuard as E, Guard as G } from '../../guard/index.mjs';
import { BuildSchema, CheckSchema, ErrorSchema } from './schema.mjs';
import { Unique } from './_unique.mjs';
// ------------------------------------------------------------------
// Build
// ------------------------------------------------------------------
function BuildOneOfStandard(stack, context, schema, value) {
    return Reducer(stack, context, schema.oneOf, value, E.IsEqual(E.Member('results', 'length'), E.Constant(1)));
}
function BuildOneOfFast(stack, context, schema, value) {
    const [result] = [Unique()];
    const results = E.ArrayLiteral(schema.oneOf.map((schema) => BuildSchema(stack, context, schema, value)));
    const count = E.Counted(results, [result, '_'], E.IsEqual(result, E.Constant(true)));
    return E.IsEqual(count, E.Constant(1));
}
export function BuildOneOf(stack, context, schema, value) {
    return context.UseUnevaluated()
        ? BuildOneOfStandard(stack, context, schema, value)
        : BuildOneOfFast(stack, context, schema, value);
}
// ------------------------------------------------------------------
// Check
// ------------------------------------------------------------------
export function CheckOneOf(stack, context, schema, value) {
    const passedContexts = schema.oneOf.reduce((result, schema) => {
        const nextContext = new CheckContext();
        return CheckSchema(stack, nextContext, schema, value) ? [...result, nextContext] : result;
    }, []);
    return G.IsEqual(passedContexts.length, 1) && context.Merge(passedContexts);
}
// ------------------------------------------------------------------
// Error
// ------------------------------------------------------------------
export function ErrorOneOf(stack, context, schemaPath, instancePath, schema, value) {
    const failedContexts = [];
    const passingSchemas = [];
    const passedContexts = schema.oneOf.reduce((result, schema, index) => {
        const nextContext = new ErrorContext();
        const nextSchemaPath = `${schemaPath}/oneOf/${index}`;
        const isSchema = ErrorSchema(stack, nextContext, nextSchemaPath, instancePath, schema, value);
        if (isSchema)
            passingSchemas.push(index);
        if (!isSchema)
            failedContexts.push(nextContext);
        return isSchema ? [...result, nextContext] : result;
    }, []);
    const isOneOf = G.IsEqual(passedContexts.length, 1) && context.Merge(passedContexts);
    if (!isOneOf && G.IsEqual(passingSchemas.length, 0))
        failedContexts.forEach(failed => failed.GetErrors().forEach(error => context.AddError(error)));
    return isOneOf || context.AddError({
        keyword: 'oneOf',
        schemaPath,
        instancePath,
        params: { passingSchemas },
    });
}
