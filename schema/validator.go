package schema

// Ports packages/schema/src/validator.ts

import (
	"errors"
	"sync"
)

// Compile compiles schema into a Validator. It fails if the schema is malformed or a `$ref` names nothing (register other documents with options.Schemas). The schema is read, never modified.
func Compile(schema JsonSchema, options CompileOptions) (*Validator, error) {
	var v *Validator
	err := recoverCompile(func() {
		c := compile(schema, options)
		v = &Validator{check: c.root(), schema: schema, options: options}
		if c.dynamic {
			v.serial, v.dynamicCompiler = true, c
		}
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Validator is a compiled schema. It is safe for concurrent use.
//
// A validator whose schema has a bookended `$dynamicRef` tracks the dynamic scope in state that a check mutates, and compiles the dynamic targets on first use, so its checks run one at a time.
type Validator struct {
	check   Check
	schema  JsonSchema
	options CompileOptions

	serial          bool
	mu              sync.Mutex
	dynamicCompiler *compiler

	explainerOnce sync.Once
	explainer     *explainer
	explainerErr  error
}

// Check reports whether value is valid. The error is the one a `$dynamicRef` target that is malformed or names nothing raises when a check first reaches it, as that compile error does upstream; every other validator never fails.
func (v *Validator) Check(value any) (bool, error) {
	if !v.serial {
		return v.check(value), nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.checkSerial(value)
}

func (v *Validator) checkSerial(value any) (valid bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			v.dynamicCompiler.reset()
			err = compileFailure(r)
		}
	}()
	return v.check(value), nil
}

// Errors explains why value is invalid: one error per failing keyword, none when it is valid. The explanation is built on the first failure and kept, so a validator whose values always pass never pays for it. The error is the one the explanation raises where Pi's `errors` throws: a subschema that is neither an object nor a boolean, or a reference that names nothing, in a part of the schema the check never compiled.
func (v *Validator) Errors(value any) ([]ValidationError, error) {
	valid, err := v.Check(value)
	if err != nil || valid {
		return nil, err
	}
	v.explainerOnce.Do(func() {
		v.explainerErr = recoverCompile(func() {
			v.explainer = newExplainer(compile(v.schema, v.options), v.schema)
		})
	})
	if v.explainerErr != nil {
		return nil, v.explainerErr
	}
	return v.explainer.errors(value)
}

// compileFailure is the error of a recovered compile failure; any other panic continues.
func compileFailure(r any) error {
	failure, ok := r.(compileError)
	if !ok {
		panic(r)
	}
	return errors.New(failure.message)
}

// recoverCompile runs build and returns the error of a compile that failed.
func recoverCompile(build func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = compileFailure(r)
		}
	}()
	build()
	return nil
}

// compile builds the compiler of a schema. Most schemas compile without tracking the dynamic scope; one with a bookended $dynamicRef compiles again with it.
func compile(schema JsonSchema, options CompileOptions) *compiler {
	plain := newCompiler(schema, options, false)
	if restarted(func() { plain.root() }) {
		dynamic := newCompiler(schema, options, true)
		dynamic.root()
		return dynamic
	}
	return plain
}

// restarted runs build and reports whether it asked for a compile with dynamic scope tracking.
func restarted(build func()) (restart bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(restartSignal); !ok {
				panic(r)
			}
			restart = true
		}
	}()
	build()
	return false
}
