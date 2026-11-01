package schema

// ConvertOnly is pass 2 of the coercion alone, as TypeBox's Value.Convert (Pi's coerce-typebox.test.ts runs it through `new Coercer(new Compiler(schema, { formats }, false), schema).convertOnly(value)`).
func ConvertOnly(schema JsonSchema, value any) (any, error) {
	var co *coercer
	if err := recoverCompile(func() { co = newCoercer(newCompiler(schema, CompileOptions{}, false), schema) }); err != nil {
		return nil, err
	}
	return co.convertOnly(value)
}
