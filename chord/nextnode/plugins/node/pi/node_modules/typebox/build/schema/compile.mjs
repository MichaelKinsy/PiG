// deno-fmt-ignore-file
// deno-lint-ignore-file
import { Arguments } from '../system/arguments/index.mjs';
import * as Build from './build.mjs';
import { Errors } from './errors.mjs';
import { ThrowParseError } from './parse.mjs';
// ------------------------------------------------------------------
// Validator
// ------------------------------------------------------------------
export class Validator {
    constructor(context, schema) {
        this.evaluateResult = Build.Build(context, schema).Evaluate();
        this.context = context;
        this.schema = schema;
    }
    /** Returns true if this Validator is using JIT acceleration. */
    IsAccelerated() {
        return this.evaluateResult.IsAccelerated();
    }
    /** Returns the underlying Schema used to construct this Validator. */
    Schema() {
        return this.schema;
    }
    /** Performs a type-guard check on the provided value. */
    Check(value) {
        return this.evaluateResult.Check(value);
    }
    /** Validates a value and returns it. Will throw if invalid. */
    Parse(value) {
        if (this.evaluateResult.Check(value))
            return value;
        ThrowParseError(this.context, this.schema, value);
    }
    /** Returns an array of validation errors for the given value. */
    Errors(value) {
        return Errors(this.context, this.schema, value);
    }
}
/** Compiles this schema into a high performance Validator */
export function Compile(...args) {
    const [context, schema] = Arguments.Match(args, {
        2: (context, schema) => [context, schema],
        1: (schema) => [{}, schema]
    });
    return new Validator(context, schema);
}
