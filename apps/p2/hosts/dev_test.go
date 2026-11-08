package hosts

import (
	"encoding/json"
	"regexp"
	"testing"
)

func FuzzParseDevCommand(f *testing.F) {
	for _, seed := range []string{`{"action":"restart"}`, `{"action":"reload"}`, `{"action":"reload","plugins":["a","a","b-c"]}`, `[]`, `null`, `{"action":"reload","plugins":null}`} {
		f.Add([]byte(seed))
	}
	name := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
	f.Fuzz(func(t *testing.T, raw []byte) {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return
		}
		command, err := ParseDevCommand(value)
		if err != nil {
			return
		}
		if command.Action != "reload" && command.Action != "restart" {
			t.Fatalf("action %q", command.Action)
		}
		if command.Action == "restart" && command.Plugins != nil {
			t.Fatal("restart with plugins")
		}
		if len(command.Plugins) > 100 {
			t.Fatal("too many plugins")
		}
		seen := map[string]bool{}
		for _, plugin := range command.Plugins {
			if !name.MatchString(plugin) || seen[plugin] {
				t.Fatalf("plugin %q", plugin)
			}
			seen[plugin] = true
		}
	})
}
