import {
  Build,
  Clean,
  Convert,
  Create,
  Decode,
  Default,
  Encode,
  Errors,
  HasCodec,
  ParseError,
  Parser
} from "./typebox-shared-UUYEEUZA.mjs";
import {
  arguments_exports,
  settings_exports
} from "./typebox-shared-52AGGYDU.mjs";

// node_modules/typebox/build/compile/code.mjs
function TsIgnore() {
  return `// @ts-ignore`;
}
function Separator() {
  return ``;
}
function ImportSection(build) {
  const context = build.UseUnevaluated() ? [`import { CheckContext } from "typebox/schema"`] : [];
  const hashing = `import { Hashing } from "typebox/system"`;
  const format = `import { Format } from "typebox/format"`;
  const guard = `import { Guard } from "typebox/guard"`;
  return [...context, hashing, format, guard];
}
function ExternalSection(build) {
  const { identifier } = build.External();
  return [
    Separator(),
    TsIgnore(),
    `let ${identifier} = []`,
    Separator(),
    TsIgnore(),
    `export function SetExternal(external) { ${identifier} = external.variables }`
  ];
}
function FunctionSection(build) {
  return build.Functions().map((func) => [Separator(), TsIgnore(), `${func};`].join("\n"));
}
function ExportSection(build) {
  const body = build.UseUnevaluated() ? `const context = new CheckContext({}, {}); return ${build.Entry()}` : `return ${build.Entry()}`;
  return [
    Separator(),
    TsIgnore(),
    `export function Check(value) { ${body} }`
  ];
}
function Code(...args) {
  const [context, type] = arguments_exports.Match(args, {
    2: (context2, type2) => [context2, type2],
    1: (type2) => [{}, type2]
  });
  const build = Build(context, type);
  const code = [...ImportSection(build), ...ExternalSection(build), ...FunctionSection(build), ...ExportSection(build)].join("\n");
  return { External: build.External(), Code: code };
}

// node_modules/typebox/build/compile/validator.mjs
var Validator = class {
  /** Constructs a Validator. */
  constructor(context, type) {
    this.hasCodec = HasCodec(context, type);
    this.buildResult = Build(context, type);
    this.evaluateResult = this.buildResult.Evaluate();
  }
  // ----------------------------------------------------------------
  // IsAccelerated
  // ----------------------------------------------------------------
  /** Returns true if this Validator is using JIT acceleration. */
  IsAccelerated() {
    return this.evaluateResult.IsAccelerated();
  }
  // ----------------------------------------------------------------
  // Context & Type
  // ----------------------------------------------------------------
  /** Returns the Context for this validator. */
  Context() {
    return this.buildResult.Context();
  }
  /** Returns the underlying Type used to construct this Validator. */
  Type() {
    return this.buildResult.Schema();
  }
  // ----------------------------------------------------------------
  // Code
  // ----------------------------------------------------------------
  /** Returns the generated code for this validator. */
  Code() {
    return this.evaluateResult.Code();
  }
  // ----------------------------------------------------------------
  // Standard Validator
  // ----------------------------------------------------------------
  /** Performs a type-guard check on the provided value. */
  Check(value) {
    return this.evaluateResult.Check(value);
  }
  /** Validates a value and returns it. Will throw if invalid. */
  Parse(value) {
    const checked = this.Check(value);
    if (checked)
      return value;
    if (settings_exports.Get().correctiveParse)
      return Parser(this.Context(), this.Type(), value);
    throw new ParseError(value, this.Errors(value));
  }
  /** Returns an array of validation errors for the given value. */
  Errors(value) {
    if (this.IsAccelerated() && this.Check(value))
      return [];
    return Errors(this.Context(), this.Type(), value);
  }
  // ----------------------------------------------------------------
  // Value.* Operations
  // ----------------------------------------------------------------
  /** Cleans a value using the Validator type. */
  Clean(value) {
    return Clean(this.Context(), this.Type(), value);
  }
  /** Converts a value using the Validator type. */
  Convert(value) {
    return Convert(this.Context(), this.Type(), value);
  }
  /** Creates a value using the Validator type. */
  Create() {
    return Create(this.Context(), this.Type());
  }
  /** Creates defaults using the Validator type. */
  Default(value) {
    return Default(this.Context(), this.Type(), value);
  }
  /** Decodes a value */
  Decode(value) {
    const result = this.hasCodec ? Decode(this.Context(), this.Type(), value) : this.Parse(value);
    return result;
  }
  /** Encodes a value */
  Encode(value) {
    const result = this.hasCodec ? Encode(this.Context(), this.Type(), value) : this.Parse(value);
    return result;
  }
};

// node_modules/typebox/build/compile/compile.mjs
function Compile(...args) {
  const [context, type] = arguments_exports.Match(args, {
    2: (context2, type2) => [context2, type2],
    1: (type2) => [{}, type2]
  });
  return new Validator(context, type);
}
export {
  Code,
  Compile,
  Validator
};
