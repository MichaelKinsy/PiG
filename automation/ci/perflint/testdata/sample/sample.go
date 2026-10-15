package sample

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
)

type AgentMessage struct{ A int }

func RoundTrip(v map[string]int) map[string]int {
	b, _ := json.Marshal(v)
	var out map[string]int
	_ = json.Unmarshal(b, &out)
	return out
}

func Compare(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func Fallback(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	x, _ := json.Marshal(a)
	return len(x) == 0
}

func Compile(s string) bool { return regexp.MustCompile("a+").MatchString(s) }

var okPattern = regexp.MustCompile("b+")

func History(h []AgentMessage) ([]AgentMessage, []AgentMessage) {
	return slices.Clone(h), append([]AgentMessage(nil), h...)
}

func Allowed(h []AgentMessage) []AgentMessage {
	//perf:ok once per session, not per turn
	return slices.Clone(h)
}

func Env() (string, error) {
	_, err := os.Stat("x")
	return os.Getenv("X"), err
}

func Loop(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += fmt.Sprintf("%d", i)
	}
	return s
}
