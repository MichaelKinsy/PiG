// compile(): a schema to a Validator, whose `check` is the fast boolean path and whose `errors` explains failures.
import { Coercer } from "./coerce.js";
import { Compiler, RESTART } from "./compiler.js";
import { Explainer } from "./explain.js";
export function compile(schema, options = NO_OPTIONS) {
    const check = compileCheck(schema, options);
    return options.coerce === true
        ? new CoercingValidator(schema, options, check)
        : new Validator(schema, options, check);
}
const NO_OPTIONS = Object.freeze({});
/** A compiled schema whose valid values have type `T` (`unknown` unless the schema was built with the builders). */
export class Validator {
    /** Whether `value` is valid. A plain function: it may be passed around without its validator. */
    check;
    schema;
    options;
    explainer = undefined;
    constructor(schema, options, check) {
        // A compiled check is exactly the test that a value has the schema's static type.
        this.check = check;
        this.schema = schema;
        this.options = options;
    }
    /**
     * Why `value` is invalid: one error per failing keyword, `[]` when it is valid. Built on the first failure and
     * kept, so a validator whose values always pass never pays for it.
     */
    errors(value) {
        if (this.check(value))
            return [];
        this.explainer ??= new Explainer(compiler(this.schema, this.options), this.schema);
        return this.explainer.errors(value);
    }
    /**
     * `value` itself when it is valid, else its errors: check() and errors() in one result, without coercion.
     *
     * Not TypeBox's `Value.Parse`, which also clones, fills defaults, converts and strips undeclared properties before
     * it checks. This only checks: the strict replacement where `Value.Parse` is used to validate (its other steps
     * change nothing for a valid value of a schema without defaults or extra properties). Use `parse()` with
     * `coerce: true` for conversion.
     */
    validate(value) {
        return this.check(value) ? { ok: true, value } : { ok: false, errors: this.errors(value) };
    }
}
/** A validator compiled with `coerce: true`: it also coerces loosely typed input towards the schema. */
export class CoercingValidator extends Validator {
    coercer = undefined;
    /**
     * `value` coerced towards the schema: numeric strings to numbers, `"true"` to `true`, a single value to a one-item
     * array, an optional `null` dropped, the first union branch the value converts to (the README lists every rule).
     * Only the changed paths are copied; `value` comes back itself when nothing changes, and is never modified. Values
     * that cannot be coerced stay as they are. The rules are built on the first call.
     */
    coerce(value) {
        this.coercer ??= new Coercer(compiler(this.schema, this.options), this.schema);
        return this.coercer.coerce(value);
    }
    /** Coerces `value`, then checks it: the coerced value when it is valid, else its errors. */
    parse(value) {
        return this.validate(this.coerce(value));
    }
}
function compileCheck(schema, options) {
    return compiler(schema, options).root();
}
// Most schemas compile without tracking the dynamic scope; one with a bookended $dynamicRef compiles again with it.
function compiler(schema, options) {
    const plain = new Compiler(schema, options, false);
    try {
        plain.root();
        return plain;
    }
    catch (error) {
        if (error !== RESTART)
            throw error;
        return new Compiler(schema, options, true);
    }
}
//# sourceMappingURL=validator.js.map