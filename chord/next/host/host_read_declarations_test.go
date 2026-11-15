package host

import (
	"strings"
	"testing"
)

// Go tests of packages/chord/src/host/read-declarations.ts's pure checks, which Pi covers through the refusals of
// test/host/named-query.test.ts and query-reader.test.ts (they wait for the store's read engine). Pi's checks of the
// builders' and the sources' shapes (an object of functions, `[[query, args]]`) are typed in Go and have no check.

func TestReadDeclarationRefusals(t *testing.T) {
	cases := []struct {
		name string
		got  error
		want string
	}{
		{"a table given to a reader", namedQueryExpected("app.todos", "liveQuery"), "app.todos: self.liveQuery reads a named query; a table of this object is read with self.query"},
		{"a list's reader names the list", namedQueryExpected("app.todos", `list "all"`), `app.todos: self.list "all" reads a named query; a table of this object is read with self.query`},
		{"a named query given to self.query", tableExpected("app.todos"), "app.todos: self.query reads a table of this object; a named query is read with self.liveQuery"},
		{"a table the factory did not declare", undeclaredTable("app.todos", "queries"), "app.todos: self.queries: the table is not one this factory declared"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got == nil || c.got.Error() != c.want {
				t.Fatalf("error = %v, want %q", c.got, c.want)
			}
		})
	}
}

func TestFirstOf(t *testing.T) {
	for _, first := range []int64{1, 25, 1<<53 - 1} {
		if err := firstOf("app.todos", "liveQuery", first); err != nil {
			t.Fatalf("firstOf(%d) = %v, want accepted", first, err)
		}
	}
	for _, first := range []int64{0, -1, 1 << 53} {
		err := firstOf("app.todos", "list", first)
		if err == nil || err.Error() != "app.todos: self.list: { first: n } is required, a positive integer" {
			t.Fatalf("firstOf(%d) = %v, want the refusal", first, err)
		}
	}
}

func TestListName(t *testing.T) {
	listed := map[string]struct{}{}
	if err := listName("app.todos", "all", listed); err != nil {
		t.Fatalf("a first name: %v", err)
	}
	if err := listName("app.todos", "all", listed); err == nil || err.Error() != `app.todos: list "all" is declared twice` {
		t.Fatalf("a second declaration: %v", err)
	}
	if err := listName("app.todos", "", listed); err == nil || !strings.HasSuffix(err.Error(), "self.list: a list has a name") {
		t.Fatalf("an empty name: %v", err)
	}
	if _, kept := listed[""]; kept {
		t.Fatal("a refused name was remembered")
	}
	if err := listName("app.todos", "open", listed); err != nil {
		t.Fatalf("another name: %v", err)
	}
}
