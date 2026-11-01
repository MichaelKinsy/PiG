// deno-fmt-ignore-file
import { Arguments } from '../../system/arguments/index.mjs';
import { Errors as SchemaErrors } from '../../schema/index.mjs';
/** Returns an array of validation errors for the given value. */
export function Errors(...args) {
    const [context, type, value] = Arguments.Match(args, {
        3: (context, type, value) => [context, type, value],
        2: (type, value) => [{}, type, value],
    });
    const [_, errors] = SchemaErrors(context, type, value);
    return errors;
}
