package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	for _, nested := range []bool{false, true} {
		a := extension.Extension{Path: "first"}
		a.InitializeEventHandlers()
		b := extension.Extension{Path: "second"}
		b.InitializeEventHandlers()
		calls := []string{}
		var runner *inproc.Runner
		emit := func() {
			if _, err := runner.Emit(context.Background(), extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{}}); err != nil {
				panic(err)
			}
		}
		a.AddEventHandler("agent_end", 1, func(...any) (any, error) {
			calls = append(calls, "A")
			b.RemoveEventHandler("agent_end", 2)
			b.AddEventHandler("agent_end", 3, func(...any) (any, error) { calls = append(calls, "C"); return nil, nil })
			if nested {
				a.RemoveEventHandler("agent_end", 1)
				emit()
			}
			return nil, nil
		})
		b.AddEventHandler("agent_end", 2, func(...any) (any, error) { calls = append(calls, "B"); return nil, nil })
		runner = inproc.NewRunner([]extension.Extension{a, b}, ".")
		emit()
		if !nested {
			emit()
		}
		raw, err := json.Marshal(calls)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(raw))
	}
}
