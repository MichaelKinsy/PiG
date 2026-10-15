package evals

// Pins the credential hand-off of harness.ts runPiCodingAgent (the agent's auth.json holds a command that fetches the API
// key from the bridge). The case needs no pig process, so it runs on every platform, including the Windows quoting of
// shellQuote.

import (
	"encoding/json"
	"runtime"
	"testing"
)

func TestAPIKeyHandOff(t *testing.T) {
	command := "!" + shellQuote("/opt/x/pig-eval-extension") + " credential"
	cases := []struct {
		name, stored, apiKey, wantEntry, wantServed string
	}{
		{"stored API key", `{"type":"api_key","key":"$KEY","env":{"KEY":"v"}}`, "resolved", `{"env":{"KEY":"v"},"key":` + strconvQuote(command) + `,"type":"api_key"}`, "resolved"},
		{"environment API key", ``, "from-env", `{"key":` + strconvQuote(command) + `,"type":"api_key"}`, "from-env"},
		{"OAuth stays as stored", `{"type":"oauth","access":"a","refresh":"r","expires":1}`, "a", `{"type":"oauth","access":"a","refresh":"r","expires":1}`, ""},
		{"no credential", ``, "", ``, ""},
	}
	for _, c := range cases {
		var stored json.RawMessage
		if c.stored != "" {
			stored = json.RawMessage(c.stored)
		}
		entry, served, err := apiKeyHandOff(stored, c.apiKey, "/opt/x/pig-eval-extension")
		if err != nil || string(entry) != c.wantEntry || served != c.wantServed {
			t.Errorf("%s: entry = %s, served = %q, err = %v; want %s, %q", c.name, entry, served, err, c.wantEntry, c.wantServed)
		}
	}
	if _, _, err := apiKeyHandOff(nil, "k", ""); err == nil || err.Error() != "Set PI_EVAL_EXTENSION to the pig-eval-extension binary." {
		t.Errorf("missing extension: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := shellQuote("/a b/it's"); got != `'/a b/it'\''s'` {
			t.Errorf("shellQuote = %s", got)
		}
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
