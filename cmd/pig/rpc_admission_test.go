package main

import (
	"reflect"
	"testing"
)

// Promise reactions are queued together, in registration order, before a reaction can add another reaction.
func TestRPCPromiseReactionOrder(t *testing.T) {
	var got []int
	turn := &rpcResponseTurn{write: func(any) {}}
	p := &rpcPromise[int]{turn: turn}
	p.then(func(v int, _ error) { got = append(got, v); p.then(func(v int, _ error) { got = append(got, v+2) }) })
	p.then(func(v int, _ error) { got = append(got, v+1) })
	p.resolve(1, nil)
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatal(got)
	}
	turn.begin()
	p.then(func(v int, _ error) { got = append(got, v+3) })
	if len(got) != 3 {
		t.Fatalf("fulfilled await ran inside the input callback: %v", got)
	}
	turn.end()
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatal(got)
	}
}
