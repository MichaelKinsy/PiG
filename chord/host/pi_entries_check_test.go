package host

// Runtime checks that stand in for Pi's compile-time parameter types of self.on, self.task and self.state: Go takes the
// declarations through `any` (Entry.Value, the listener's event and select value), so these tests pin that a value of the
// wrong kind is refused as Pi's own runtime checks refuse it (object.ts:1503-1602).

import (
	"errors"
	"testing"
)

// serveFactoryError serves a counter contract whose factory runs declare and returns the factory's error from the first get.
func serveFactoryError(t *testing.T, declare func(self Self) error) error {
	t.Helper()
	host := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	factory := func(_ any, _ Principal, self Self) (Implementation, error) {
		if err := declare(self); err != nil {
			return nil, err
		}
		return nil, errors.New("declared")
	}
	if err := host.Serve(linkCounter, factory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := host.Hold(linkCounter, map[string]any{"name": "c"}, nil)
	return err
}

// Pi packages/chord/src/object.ts:1597: self.task throws TypeError `a task must be a function` for a value that is not a
// function; Go's Task takes a nil function as that value (the parameter type is the compile-time part).
func TestSelfTaskRefusesAFunctionThatIsMissing(t *testing.T) {
	var got error
	err := serveFactoryError(t, func(self Self) error {
		got = self.Task(nil)
		return got
	})
	var typeErr *TypeError
	if !errors.As(got, &typeErr) || typeErr.Message != "a task must be a function" {
		t.Fatalf("Task error = %v (get: %v), want TypeError a task must be a function", got, err)
	}
}

// Pi packages/chord/src/object.ts:1586: self.on throws TypeError `listener <name>: the handler must be a function`.
func TestSelfOnRefusesAHandlerThatIsMissing(t *testing.T) {
	var got error
	err := serveFactoryError(t, func(self Self) error {
		got = self.On("follow", nil, nil)
		return got
	})
	var typeErr *TypeError
	if !errors.As(got, &typeErr) || typeErr.Message != "listener follow: the handler must be a function" {
		t.Fatalf("On error = %v (get: %v), want TypeError listener follow: the handler must be a function", got, err)
	}
}

// Pi packages/chord/src/object.ts:1503: a declaration with an empty name throws TypeError `<kind> needs a name`.
func TestSelfDeclarationRefusesAnEmptyName(t *testing.T) {
	var got error
	err := serveFactoryError(t, func(self Self) error {
		_, got = self.State("", linkCountSchema, nil, nil)
		return got
	})
	var typeErr *TypeError
	if !errors.As(got, &typeErr) || typeErr.Message != "state needs a name" {
		t.Fatalf("State error = %v (get: %v), want TypeError state needs a name", got, err)
	}
}
