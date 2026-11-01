// deno-fmt-ignore-file
import { Arguments } from '../system/arguments/index.mjs';
import { Get as GetLocalizationFunction } from '../system/locale/_config.mjs';
import * as Engine from './engine/index.mjs';
/** Returns an array of validation errors for the given value. */
export function Errors(...args) {
    const [context, schema, value] = Arguments.Match(args, {
        3: (context, schema, value) => [context, schema, value],
        2: (schema, value) => [{}, schema, value]
    });
    const stack = new Engine.Stack(context, schema);
    const errorContext = new Engine.ErrorContext();
    const result = Engine.ErrorSchema(stack, errorContext, '#', '', schema, value);
    const errors = errorContext.GetErrors();
    const locale = GetLocalizationFunction();
    const localized = errors.map(error => ({ ...error, message: locale(error) }));
    return [result, localized];
}
