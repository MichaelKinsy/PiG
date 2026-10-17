// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"strings"
	"testing"
)

// A Context getter whose result is a call ARGUMENT is drifted too: the compiler reports the arity error at the argument ("multiple-value
// ctx.ConfigHome() (value of type (string, error)) in single-value context", or "cannot use ... as string value in argument"), not at an assignment.
// The rule hoists the call before the statement that holds it, as it does for a call inside fmt.Println.
func TestGetterAsACallArgumentHoistsTheError(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"sole argument", `func use(dir string) {}

func handler(ctx sdk.Context) error {
	use(ctx.ConfigHome())
	return nil
}
`, `func use(dir string) {}

func handler(ctx sdk.Context) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	use(configHome)
	return nil
}
`},
		{"middle argument", `func join3(a, b, c string) string { return a + b + c }

func handler(ctx sdk.Context) (string, error) {
	return join3("a", ctx.ConfigHome(), "c"), nil
}
`, `func join3(a, b, c string) string { return a + b + c }

func handler(ctx sdk.Context) (string, error) {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return "", err
	}
	return join3("a", configHome, "c"), nil
}
`},
		{"if initializer", `type store struct{}

func (store) save(dir string) error { return nil }

func handler(ctx sdk.Context, c store) error {
	if err := c.save(ctx.ConfigHome()); err != nil {
		return err
	}
	return nil
}
`, `type store struct{}

func (store) save(dir string) error { return nil }

func handler(ctx sdk.Context, c store) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	if err := c.save(configHome); err != nil {
		return err
	}
	return nil
}
`},
		{"sole argument of a variadic callee", `func join(dirs ...string) string { return "" }

func handler(ctx sdk.Context) error {
	_ = join(ctx.ConfigHome())
	return nil
}
`, `func join(dirs ...string) string { return "" }

func handler(ctx sdk.Context) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	_ = join(configHome)
	return nil
}
`},
		{"switch initializer", `func handler(ctx sdk.Context) error {
	switch dir := os.Getenv(ctx.ConfigHome()); dir {
	case "a":
	}
	return nil
}
`, `func handler(ctx sdk.Context) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	switch dir := os.Getenv(configHome); dir {
	case "a":
	}
	return nil
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := planSource(t, tc.body)
			if got := rewritten(t, result); got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// Moving a getter out of an if or switch with an init statement is only safe when the getter is inside the init statement and nothing in it runs
// before the getter: a getter in the condition would run before the init statement, and an earlier call in the init statement would run after it.
func TestGetterInAnIfWithAnInitStatementIsHoistedOnlyWhenOrderSurvives(t *testing.T) {
	for _, tc := range []struct{ name, body, reason string }{
		{"getter in the condition", `func handler(ctx sdk.Context) error {
	if ok := os.Getenv("A"); ok == ctx.ConfigHome() {
		return nil
	}
	return nil
}
`, "init statement"},
		{"earlier call in the init statement", `func other() string { return "" }

func two(a, b string) error { return nil }

func handler(ctx sdk.Context) error {
	if err := two(other(), ctx.ConfigHome()); err != nil {
		return err
	}
	return nil
}
`, "earlier call"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := planSource(t, tc.body)
			if len(result.Files) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, tc.reason) {
				t.Fatalf("files = %d, skipped = %+v, want no rewrite and one skip mentioning %q", len(result.Files), result.Skipped, tc.reason)
			}
		})
	}
}
