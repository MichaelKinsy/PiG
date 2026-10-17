package host

// Ports packages/chord/src/declare.ts: what a factory declares through self besides state cells, the shapes of
// resources and listeners. Pi declares them by name (`self.resource(name, declaration)`, `self.on(name, source, …)`);
// the descriptor factories cell, resource, listener and derivation, and their types, are gone. EventOf and ValueOf are
// type-level only and have no Go form.

import "context"

// Subscribable is an event source.
type Subscribable[E any] interface {
	// Subscribe registers listener and returns the unsubscribe function.
	Subscribe(listener func(event E)) func()
}

// ResourceOpenOptions is the argument of ResourceDeclaration.Open (`{ readonly signal: AbortSignal }`).
type ResourceOpenOptions struct {
	Signal context.Context
}

// ResourceDeclaration is what Self.Resource declares: something the object opens and later closes (a database, a
// watch, a socket).
type ResourceDeclaration[T any] struct {
	// Version is bumped to replace the resource at the next reload (a native value has no schema to detect a change).
	Version int
	// Deps are references or resources that must be live/open first; a change replaces the resource. Nil is undefined.
	Deps []any
	// Exclusive resources are replaced at the gate: old closed before new opened. Pi reads `exclusive === true`, so
	// false and undefined are the same.
	Exclusive bool
	// Open opens the resource. Pi awaits its `T | Promise<T>`, so it is a blocking call; a throw or rejection is the
	// error.
	Open func(options ResourceOpenOptions) (T, error)
	// Close is optional: nil is undefined. Pi awaits it; a throw or rejection is the error.
	Close func(value T) error
}

// ResourceDeclarer is a ResourceDeclaration of any value type: what Self.Resource takes, as Pi's generic
// `resource<T>(name, declaration: ResourceDeclaration<T>)`.
type ResourceDeclarer interface {
	resourceDeclaration() ResourceDeclaration[any]
}

// resourceDeclaration is the declaration as ResourceDeclaration<unknown>: Open's value is boxed, and Close receives the
// value Open returned. An absent Close stays nil.
func (d ResourceDeclaration[T]) resourceDeclaration() ResourceDeclaration[any] {
	erased := ResourceDeclaration[any]{Version: d.Version, Deps: d.Deps, Exclusive: d.Exclusive}
	if d.Open != nil {
		erased.Open = func(options ResourceOpenOptions) (any, error) { return d.Open(options) }
	}
	if d.Close != nil {
		// A nil value (Open returned undefined) is T's zero value: value.(T) would panic for an interface T.
		erased.Close = func(value any) error { return d.Close(as[T](value)) }
	}
	return erased
}

// Resource is an object's opened resource.
type Resource[T any] interface {
	Key() string
	// Value is the opened value; the error is not_ready until it is opened.
	Value() (T, error)
}

// ListenerDecl is a listener as Self.On declares it.
type ListenerDecl struct {
	Source any
	// Select is nil (Pi's undefined) when the listener subscribes to the source itself.
	Select func(value any) Subscribable[any]
	// Fn runs for each event in a fresh root. Pi awaits it; a throw or rejection is the error.
	Fn func(event any, ctx Ctx) error
}
