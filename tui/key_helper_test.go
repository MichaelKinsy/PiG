package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// keyHelperOracle is the output of upstream keys.ts Key (v1.0.4) for every property, probed with node: a string property as is, a
// builder called with "X".
const keyHelperOracle = `{"escape":"escape","esc":"esc","enter":"enter","return":"return","tab":"tab","space":"space","backspace":"backspace","delete":"delete","insert":"insert","clear":"clear","home":"home","end":"end","pageUp":"pageUp","pageDown":"pageDown","up":"up","down":"down","left":"left","right":"right","f1":"f1","f2":"f2","f3":"f3","f4":"f4","f5":"f5","f6":"f6","f7":"f7","f8":"f8","f9":"f9","f10":"f10","f11":"f11","f12":"f12","backtick":"` + "`" + `","hyphen":"-","equals":"=","leftbracket":"[","rightbracket":"]","backslash":"\\","semicolon":";","quote":"'","comma":",","period":".","slash":"/","exclamation":"!","at":"@","hash":"#","dollar":"$","percent":"%","caret":"^","ampersand":"&","asterisk":"*","leftparen":"(","rightparen":")","underscore":"_","plus":"+","pipe":"|","tilde":"~","leftbrace":"{","rightbrace":"}","colon":":","lessthan":"<","greaterthan":">","question":"?","ctrl":"ctrl+X","shift":"shift+X","alt":"alt+X","super":"super+X","ctrlShift":"ctrl+shift+X","shiftCtrl":"shift+ctrl+X","ctrlAlt":"ctrl+alt+X","altCtrl":"alt+ctrl+X","shiftAlt":"shift+alt+X","altShift":"alt+shift+X","ctrlSuper":"ctrl+super+X","superCtrl":"super+ctrl+X","shiftSuper":"shift+super+X","superShift":"super+shift+X","altSuper":"alt+super+X","superAlt":"super+alt+X","ctrlShiftAlt":"ctrl+shift+alt+X","ctrlShiftSuper":"ctrl+shift+super+X"}`

// lowerFirst maps a Go member name to the upstream property name (Key.PageUp is pageUp, Key.CtrlShiftAlt is ctrlShiftAlt).
func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }

func TestKeyMatchesUpstreamKeyObject(t *testing.T) {
	want := map[string]string{}
	if err := json.Unmarshal([]byte(keyHelperOracle), &want); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	v := reflect.ValueOf(Key)
	for i := range v.NumField() {
		got[lowerFirst(v.Type().Field(i).Name)] = v.Field(i).String()
	}
	typ := reflect.TypeOf(Key)
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		out := reflect.ValueOf(Key).Method(i).Call([]reflect.Value{reflect.ValueOf("X")})
		got[lowerFirst(m.Name)] = out[0].String()
	}
	if len(got) != len(want) {
		t.Errorf("Key has %d members, upstream has %d", len(got), len(want))
	}
	for name, w := range want {
		if g, ok := got[name]; !ok || g != w {
			t.Errorf("Key.%s = %q (present %v), want %q", name, g, ok, w)
		}
	}
}

func TestKeyBuildersMatchInput(t *testing.T) {
	if !MatchesKeyID("\x03", Key.Ctrl("c")) {
		t.Fatal("Key.Ctrl(\"c\") does not match ctrl+c input")
	}
	if MatchesKeyID("\x03", Key.Alt("c")) {
		t.Fatal("Key.Alt(\"c\") matched ctrl+c input")
	}
}
