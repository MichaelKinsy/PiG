package link_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/link/message.ts
// The message schema of the event kinds and the event ack (Pi 8d30e6306): `ack.events` and `hello.calls` are at least 1
// when present (the schema's `int({ min: 1 })`), an event's hold, member and `at` are from 0.

func TestEventSchema(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		refused := func(name string, message link.Message) {
			t.Run("refuse "+name, func(t *testing.T) {
				decoded := decode(t, codec, encode(t, codec, message))
				_, err := link.CheckMessage(decoded, background)
				requireErrorContains(t, err, "message schema")
			})
		}
		refused("an event ack of 0", &link.AckMessage{Events: some(0)})
		refused("an event with an origin 0", &link.EventMessage{Origin: some(0)})
	})
}
