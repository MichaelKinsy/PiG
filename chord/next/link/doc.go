// Package link ports packages/chord/src/next/link: Chord v2's links for its layers (a host's end of a link with its
// handshake, codec, checks, heartbeat and in-order handling), the message types, parts with their limits and
// compression, errors in messages, and the in-memory, IPC and WebSocket links.
//
// Go forced differences, each recorded where it applies:
//   - Messages are a sealed interface (Message) of one struct per kind, built by the codecs and checked by
//     CheckMessage; upstream's TypeBox `MESSAGE` schema is checked on the typed struct, field by field.
//   - A Part is a sealed interface: Text (JSON text), Bytes (received bytes) or *DeflatedPart.
//   - `string | Uint8Array | undefined` unions are a Part holding Text or Bytes; an absent optional is the zero value
//     (nil part, nil slice, "" for the incarnations whose pattern excludes the empty string) or Optional for numbers.
//   - A promise is a Handling, resolved by whoever finishes the work; an awaited promise is a blocking call
//     (InflateAsync), `AbortSignal` is a context.Context.
//   - A listener removal is the function its registration returns (a Go func has no identity to remove by).
//   - Like Pi's, the types here have no locks around what the host serializes (Channel handles messages one at a
//     time, from the goroutine that delivers or settles them); the link ends guard their own state because a
//     transport's callbacks arrive on their own goroutines.
package link
