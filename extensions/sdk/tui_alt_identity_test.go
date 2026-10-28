package sdk

import (
	"strings"
	"testing"
)

type identityPtr struct{ text string }

func (c *identityPtr) Render(int) []string { return []string{c.text} }
func (c *identityPtr) Invalidate()         {}

// identityValue is comparable but not a pointer: Pi compares by reference, so it is rejected.
type identityValue struct{ text string }

func (c identityValue) Render(int) []string { return []string{c.text} }
func (c identityValue) Invalidate()         {}

// identitySlice is not comparable; passed by value it must be rejected without a panic.
type identitySlice struct{ lines []string }

func (c identitySlice) Render(int) []string { return c.lines }
func (c identitySlice) Invalidate()         {}

// identityScreen has no extension, so any host call nil-dereferences: an error that comes first proves no call was made.
func identityScreen() *TuiAltScreen {
	return &TuiAltScreen{keys: map[Component]string{}, byKey: map[string]Component{}}
}

func TestTuiAltComponentKeysFollowPointerIdentity(t *testing.T) {
	s := identityScreen()
	a, b := &identityPtr{text: "x"}, &identityPtr{text: "x"}
	fa, err := s.componentFields(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := s.componentFields(b)
	if err != nil {
		t.Fatal(err)
	}
	if fa["key"] == fb["key"] {
		t.Fatalf("distinct pointers to equal structs share key %v", fa["key"])
	}
	again, err := s.componentFields(a)
	if err != nil {
		t.Fatal(err)
	}
	if again["key"] != fa["key"] {
		t.Fatalf("the same pointer got key %v then %v", fa["key"], again["key"])
	}
}

func TestTuiAltComponentFieldsRejectsNonPointers(t *testing.T) {
	var typedNil *identityPtr
	cases := []struct {
		name      string
		component Component
		want      string
	}{
		{"comparable value", identityValue{text: "x"}, "pointer"},
		{"non-comparable value", identitySlice{lines: []string{"x"}}, "pointer"},
		{"typed nil pointer", typedNil, "nil"},
		{"nil interface", nil, "nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := identityScreen()
			fields, err := s.componentFields(tc.component)
			if err == nil || fields != nil {
				t.Fatalf("componentFields = %v, %v; want an error", fields, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if len(s.keys) != 0 || len(s.byKey) != 0 {
				t.Fatalf("a rejected component was registered: %v", s.keys)
			}
		})
	}
}

func TestTuiAltMethodsRejectNonPointersBeforeAnyHostCall(t *testing.T) {
	value := identityValue{text: "x"}
	var typedNil *identityPtr
	calls := map[string]func(s *TuiAltScreen, c Component) error{
		"AddChild":      func(s *TuiAltScreen, c Component) error { return s.AddChild(c) },
		"RemoveChild":   func(s *TuiAltScreen, c Component) error { return s.RemoveChild(c) },
		"SetFocus":      func(s *TuiAltScreen, c Component) error { return s.SetFocus(c) },
		"SetLayoutRoot": func(s *TuiAltScreen, c Component) error { return s.SetLayoutRoot(c) },
		"ShowOverlay": func(s *TuiAltScreen, c Component) error {
			handle, err := s.ShowOverlay(c, nil)
			if handle != nil {
				t.Error("ShowOverlay returned a handle with an error")
			}
			return err
		},
		"Unfocus": func(s *TuiAltScreen, c Component) error {
			return (&TuiOverlayHandle{screen: s, key: "o1"}).Unfocus(UnfocusOptions{Target: c})
		},
	}
	for name, call := range calls {
		for label, component := range map[string]Component{"value": value, "typed nil": typedNil} {
			t.Run(name+"/"+label, func(t *testing.T) {
				err := call(identityScreen(), component)
				if err == nil {
					t.Fatal("no error")
				}
				if label == "value" && !strings.Contains(err.Error(), "pointer") {
					t.Fatalf("error %q does not mention pointer", err)
				}
			})
		}
	}
}
