package ai

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// The federation token response reads expires_in with Number(data.expires_in) (the Anthropic SDK oidc-federation.ts:125). jsnumber.FromJSON must give Number(JSON.parse(text)) for every JSON value, including strings that Number reads as hexadecimal, octal or binary integers and strings Go's ParseFloat reads differently ("0x1p3", "1_0").
func TestFederationExpiresInNumberMatchesJS(t *testing.T) {
	inputs := []string{
		`3600`, `3600.5`, `1e3`, `-0`, `1e999`, `"3600"`, `" 3600 "`, `"\u00a03600\ufeff"`, `""`, `"   "`,
		`"0x10"`, `"0X1f"`, `"0b11"`, `"0o17"`, `"-0x10"`, `"0x"`, `"0x1p3"`, `"1_0"`, `"inf"`, `"Infinity"`, `"-Infinity"`, `"+Infinity"`,
		`"infinity"`, `"nan"`, `"NaN"`, `".5"`, `"5."`, `"1e3"`, `"1e"`, `"+5"`, `"--5"`, `"12abc"`,
		`null`, `true`, `false`, `[]`, `[5]`, `["7"]`, `[1,2]`, `{}`, `{"a":1}`,
	}
	var want []string
	pioracle.Run(t, `
emit(input.map((text) => { const n = Number(JSON.parse(text)); return Object.is(n, -0) ? "-0" : String(n); }));`, inputs, &want)
	for i, text := range inputs {
		got := jsnumber.FromJSON(json.RawMessage(text))
		if !sameJSNumber(got, want[i]) {
			t.Errorf("jsnumber.FromJSON(%s) = %v, Number(JSON.parse) = %s", text, got, want[i])
		}
	}
	if got := jsnumber.FromJSON(nil); !math.IsNaN(got) {
		t.Errorf("an absent member = %v, want NaN", got)
	}
}

func sameJSNumber(got float64, want string) bool {
	switch want {
	case "NaN":
		return math.IsNaN(got)
	case "Infinity":
		return math.IsInf(got, 1)
	case "-Infinity":
		return math.IsInf(got, -1)
	case "-0":
		return got == 0 && math.Signbit(got)
	}
	parsed, err := strconv.ParseFloat(want, 64)
	return err == nil && got == parsed && !math.Signbit(got) == !math.Signbit(parsed)
}
