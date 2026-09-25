package pico3

import "testing"

func TestConversationObservationLoadedNewAndUnsubscribed(t *testing.T) {
	env := openEnv(t, openOptions{})
	var ids []Id
	stop := env.h.OnConversation(func(conversation *ConversationHandle) { ids = append(ids, conversation.Id) })
	defer stop()
	equal(t, ids, []Id{env.root.Id}, "loaded root")
	child := must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	equal(t, ids, []Id{env.root.Id, child.Id}, "new conversation")
	stop()
	must(env.h.CreateConversation(bg, ConversationSpec{}, nil))
	equal(t, ids, []Id{env.root.Id, child.Id}, "unsubscribed")
}
