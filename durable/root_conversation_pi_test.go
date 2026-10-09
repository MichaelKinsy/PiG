package durable

import "testing"

// pi: packages/durable/src/types.ts

// types.ts:39 `export const ROOT_CONVERSATION_ID = 1 as ConversationId`: the root conversation is the reserved ID 1, the first one a store
// assigns, and a Session's root conversation is addressed by it on the wire and in storage.
func TestRootConversationIdIsTheReservedOne(t *testing.T) {
	if ROOT_CONVERSATION_ID != 1 {
		t.Fatalf("ROOT_CONVERSATION_ID = %d, want 1", ROOT_CONVERSATION_ID)
	}
	var id ConversationId = 1
	if id != ROOT_CONVERSATION_ID {
		t.Fatal("the literal 1 is not the root conversation")
	}
}
