package schema

// Ports packages/schema/src/validator.ts
//
// Compile: a schema to a Validator, whose Check is the fast boolean path and whose Errors explains failures.

import (
	"errors"
	"sync"
)

// Compile compiles schema into a Validator. It fails if the schema is malformed or a `$ref` names nothing (register other documents with options.Schemas). The schema is read, never modified.
func Compile(schema JsonSchema, options ...CompileOptions) (*Validator, error) {
	var opts CompileOptions
	if len(options) > 0 {
		opts = options[0]
	}
	var v *Validator
	err := recoverCompile(func() {
		c := compile(schema, opts)
		v = &Validator{check: c.root(), schema: schema, options: opts, serial: c.dynamic}
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Validator is a compiled schema.
//
// A validator whose schema has a bookended `$dynamicRef` tracks the dynamic scope in state that a check mutates, so its checks run one at a time; every other validator checks concurrently.
type Validator struct {
	check   Check
	schema  JsonSchema
	options CompileOptions
	serial  bool
	mu      sync.Mutex

	explainerOnce sync.Once
	explainer     *explainer
	explainerErr  error
}

// Check reports whether value is valid. It is a method value that may be passed around without the validator: the function keeps the validator alive.
func (v *Validator) Check(value any) bool {
	if v.serial {
		v.mu.Lock()
		defer v.mu.Unlock()
	}
	return v.check(value)
}

// Errors explains why value is invalid: one error per failing keyword, none when it is valid. The explanation is built on the first failure and kept, so a validator whose values always pass never pays for it.
func (v *Validator) Errors(value any) []ValidationError {
	if v.Check(value) {
		return nil
	}
	v.explainerOnce.Do(func() {
		v.explainerErr = recoverCompile(func() {
			v.explainer = newExplainer(compile(v.schema, v.options), v.schema)
		})
	})
	if v.explainerErr != nil {
		panic(v.explainerErr)
	}
	return v.explainer.errors(value)
}

// recoverCompile runs build and returns the error of a compile that failed.
func recoverCompile(build func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			failure, ok := r.(compileError)
			if !ok {
				panic(r)
			}
			err = errors.New(failure.message)
		}
	}()
	build()
	return nil
}

// compile builds the compiler of a schema. Most schemas compile without tracking the dynamic scope; one with a bookended $dynamicRef compiles again with it.
func compile(schema JsonSchema, options CompileOptions) *compiler {
	plain := newCompiler(schema, options, false)
	if restarted(func() { plain.root() }) {
		return newCompiler(schema, options, true)
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
