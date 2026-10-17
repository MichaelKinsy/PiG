package mcp_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// The golden file holds Pi's packages/mcp content conversion and JSON-RPC classification run on a deterministic
// corpus and seeded-random inputs by testdata/upstream-oracle.mjs. Regenerate it from the repository root with:
//
//	node mcp/testdata/upstream-oracle.mjs > mcp/testdata/upstream-golden.json

type oracleOutcome struct {
	OK    json.RawMessage `json:"ok"`
	Error *string         `json:"error"`
}

type oracleGolden struct {
	Content []struct {
		oracleOutcome
		Result string `json:"result"`
	} `json:"content"`
	JSONRPC []struct {
		oracleOutcome
		Message string `json:"message"`
	} `json:"jsonrpc"`
}

func loadOracle(t *testing.T) oracleGolden {
	t.Helper()
	data, err := os.ReadFile("testdata/upstream-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden oracleGolden
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func reportOracle(t *testing.T, kind string, total int, differences []string) {
	t.Helper()
	if len(differences) == 0 {
		return
	}
	shown := differences
	if len(shown) > 15 {
		shown = shown[:15]
	}
	t.Errorf("%s: %d of %d cases differ from Pi:\n%s", kind, len(differences), total, strings.Join(shown, "\n"))
}

func TestParseJSONRPCMessageMatchesPi(t *testing.T) {
	golden := loadOracle(t)
	var differences []string
	for _, c := range golden.JSONRPC {
		message, err := mcp.ParseJSONRPCMessage([]byte(c.Message))
		if err != nil {
			if c.Error == nil || !strings.HasSuffix(*c.Error, err.Error()) {
				differences = append(differences, "message "+c.Message+"\n  got  error: "+err.Error()+"\n  want "+string(c.OK)+deref(c.Error))
			}
			continue
		}
		if c.Error != nil {
			differences = append(differences, "message "+c.Message+"\n  got  ok\n  want "+*c.Error)
			continue
		}
		var want string
		_ = json.Unmarshal(c.OK, &want)
		kind := "none"
		switch {
		case message.IsResponse():
			kind = "response"
		case message.IsRequest():
			kind = "request"
		case message.IsNotification():
			kind = "notification"
		}
		var id any
		if message.ID != nil {
			id, _ = json.Marshal(message.ID)
			var decoded any
			_ = json.Unmarshal(id.([]byte), &decoded)
			id = decoded
		}
		var method any
		if kind == "request" || kind == "notification" {
			method = message.Method
		}
		got, _ := json.Marshal(map[string]any{"kind": kind, "id": id, "method": method})
		if !jsonEqualOracle(string(got), want) {
			differences = append(differences, "message "+c.Message+"\n  got  "+string(got)+"\n  want "+want)
		}
	}
	reportOracle(t, "parseJsonRpcMessage", len(golden.JSONRPC), differences)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func jsonEqualOracle(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return a == b
	}
	return reflect.DeepEqual(x, y)
}
