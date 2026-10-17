package host

// The declaration descriptor constructors of packages/chord/src/declare.ts are gone in Pi (declarations are calls by
// name on self). What remains is the declaration value types: ResourceDeclaration, read type-erased by the object
// runtime, and ListenerDecl.

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// A ResourceDeclaration of any value type is read as ResourceDeclaration<unknown>.
func TestResourceDeclarationReadTypeErased(t *testing.T) {
	var closed []int
	spec := ResourceDeclaration[int]{
		Version: 4, Deps: []any{"dep"}, Exclusive: true,
		Open:  func(ResourceOpenOptions) (int, error) { return 7, nil },
		Close: func(value int) error { closed = append(closed, value); return nil },
	}.resourceDeclaration()
	value, err := spec.Open(ResourceOpenOptions{Signal: context.Background()})
	if err != nil || value != 7 || spec.Version != 4 || !spec.Exclusive || len(spec.Deps) != 1 {
		t.Fatalf("resourceDeclaration = %+v, open %v, %v", spec, value, err)
	}
	if err := spec.Close(value); err != nil || !reflect.DeepEqual(closed, []int{7}) {
		t.Fatalf("close = %v, closed %v", err, closed)
	}
	if absent := (ResourceDeclaration[int]{}).resourceDeclaration(); absent.Close != nil {
		t.Fatal("an absent close became present")
	}
}

// Open and Close errors reach the caller of the erased declaration.
func TestResourceDeclarationErrors(t *testing.T) {
	closeErr := errors.New("close failed")
	spec := ResourceDeclaration[string]{
		Open: func(options ResourceOpenOptions) (string, error) {
			<-options.Signal.Done()
			return "opened", options.Signal.Err()
		},
		Close: func(string) error { return closeErr },
	}.resourceDeclaration()
	signal, abort := context.WithCancel(context.Background())
	abort()
	if _, err := spec.Open(ResourceOpenOptions{Signal: signal}); !errors.Is(err, context.Canceled) {
		t.Fatalf("open error = %v, want the aborted signal", err)
	}
	if err := spec.Close("opened"); !errors.Is(err, closeErr) {
		t.Fatalf("close error = %v", err)
	}
}
