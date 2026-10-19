//go:build parity

package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONSubsetMatchesNestedObjectsExactlyOtherwise(t *testing.T) {
	got := map[string]any{"type": "response", "data": map[string]any{"isStreaming": false, "messageCount": 3.0, "extra": "x"}}
	for _, tc := range []struct {
		want string
		ok   bool
	}{
		{`{"data":{"isStreaming":false,"messageCount":3}}`, true},
		{`{"data":{"messageCount":4}}`, false},
		{`{"data":{"missing":1}}`, false},
		{`{"data":3}`, false},
	} {
		want := map[string]any{}
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		if jsonSubset(want, got) != tc.ok {
			t.Fatalf("jsonSubset(%s) = %v, want %v", tc.want, !tc.ok, tc.ok)
		}
	}
}

func TestPollRPCStateResendsUntilTheStateAndDropsItsResponses(t *testing.T) {
	var out synchronizedBuffer
	counts := []int{1, 2, 3}
	sent := 0
	write := func(line string) error {
		_, _ = out.Write([]byte(`{"id":"settled","type":"response","data":{"messageCount":` + string(rune('0'+counts[sent])) + "}}\n"))
		sent++
		return nil
	}
	id, err := pollRPCState(context.Background(), &out, `{"id":"settled","type":"get_state"}`, `{"data":{"messageCount":3}}`, 5, write)
	if err != nil || id != "settled" || sent != 3 {
		t.Fatalf("id=%q sent=%d err=%v", id, sent, err)
	}
	_, _ = out.Write([]byte(`{"id":"after","type":"response"}` + "\n"))
	if got := dropPolledResponses(out.String(), []string{id}); got != `{"id":"after","type":"response"}`+"\n" {
		t.Fatalf("polled responses kept: %q", got)
	}
}

func TestPollRPCStateFailsWhenTheStateNeverArrives(t *testing.T) {
	var out synchronizedBuffer
	write := func(string) error {
		_, _ = out.Write([]byte(`{"id":"settled","type":"response","data":{"messageCount":1}}` + "\n"))
		return nil
	}
	_, err := pollRPCState(context.Background(), &out, `{"id":"settled","type":"get_state"}`, `{"data":{"messageCount":9}}`, 1, write)
	if err == nil || !strings.Contains(err.Error(), "settled") {
		t.Fatalf("poll accepted a state that never arrived: %v", err)
	}
	if _, err := pollRPCState(context.Background(), &out, `{"type":"get_state"}`, `{"data":{}}`, 1, write); err == nil {
		t.Fatal("poll accepted a line without an id")
	}
}
