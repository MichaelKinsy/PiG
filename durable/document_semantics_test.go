package durable

import (
	"strings"
	"testing"
)

type semanticsState struct{}

// panicMessage returns what define panics with, or "" when it returns.
func panicMessage(define func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message, _ = recovered.(string)
			if message == "" {
				message = "panic"
			}
		}
	}()
	define()
	return ""
}

// defineWithSemantics defines a singleton document and a document family with semantics and returns what each rejected it with.
func defineWithSemantics(semantics DocumentSemantics) (singleton, family string) {
	singleton = panicMessage(func() {
		DefineDoc(DocDefinition[semanticsState]{
			CommonDocDefinition: CommonDocDefinition[semanticsState]{Kind: "t.semantics", Version: 1, Initial: func() semanticsState { return semanticsState{} }, CheckpointWhen: func(semanticsState, []Op, CheckpointInfo) bool { return false }},
			DocumentSemantics:   semantics,
		})
	})
	family = panicMessage(func() {
		DefineDocFamily(DocFamilyDefinition[semanticsState, string]{
			Family: true, Kind: "t.semantics", Version: 1, DocumentSemantics: semantics,
			Initial:        func(string) semanticsState { return semanticsState{} },
			CheckpointWhen: func(semanticsState, []Op, CheckpointInfo) bool { return false },
		})
	})
	return singleton, family
}

// packages/durable/src/types.ts:42-60 DocumentSemantics, enforced upstream by the compiler (documents.ts:40-75 overloads): a session or task
// document declares neither history nor fork; a latest conversation document forks "current" or "initial"; a rewindable one forks "asOf",
// "current" or "initial". Go has one struct, so DefineDoc and DefineDocFamily reject the other combinations when the document is defined,
// as they reject a non-positive version.
// mutation-checked: dropping the session/task, latest or rewindable branch of validateDocumentSemantics, or the default branches, fails it.
func TestDocumentSemanticsAdmitOnlyThePiUnion(t *testing.T) {
	valid := []DocumentSemantics{
		{Scope: ScopeSession},
		{Scope: ScopeTask},
		{Scope: ScopeConversation, History: HistoryLatest, Fork: ForkCurrent},
		{Scope: ScopeConversation, History: HistoryLatest, Fork: ForkInitial},
		{Scope: ScopeConversation, History: HistoryRewindable, Fork: ForkAsOf},
		{Scope: ScopeConversation, History: HistoryRewindable, Fork: ForkCurrent},
		{Scope: ScopeConversation, History: HistoryRewindable, Fork: ForkInitial},
	}
	for _, semantics := range valid {
		if singleton, family := defineWithSemantics(semantics); singleton != "" || family != "" {
			t.Errorf("%+v rejected: %q, %q", semantics, singleton, family)
		}
	}
	invalid := map[string]DocumentSemantics{
		"latest conversation that forks asOf":       {Scope: ScopeConversation, History: HistoryLatest, Fork: ForkAsOf},
		"latest conversation without a fork":        {Scope: ScopeConversation, History: HistoryLatest},
		"rewindable conversation without a fork":    {Scope: ScopeConversation, History: HistoryRewindable},
		"rewindable conversation with another fork": {Scope: ScopeConversation, History: HistoryRewindable, Fork: "sideways"},
		"conversation without a history":            {Scope: ScopeConversation, Fork: ForkCurrent},
		"conversation with another history":         {Scope: ScopeConversation, History: "forever", Fork: ForkCurrent},
		"session with a history":                    {Scope: ScopeSession, History: HistoryLatest},
		"session with a fork":                       {Scope: ScopeSession, Fork: ForkCurrent},
		"task with a history":                       {Scope: ScopeTask, History: HistoryRewindable},
		"task with a fork":                          {Scope: ScopeTask, Fork: ForkInitial},
		"another scope":                             {Scope: "global"},
		"no scope":                                  {},
	}
	for name, semantics := range invalid {
		singleton, family := defineWithSemantics(semantics)
		for kind, message := range map[string]string{"DefineDoc": singleton, "DefineDocFamily": family} {
			if !strings.Contains(message, "Document t.semantics") || !strings.Contains(message, "semantics:") {
				t.Errorf("%s: %s did not reject it (%q)", name, kind, message)
			}
		}
	}
}

// types.ts:41-53: the two named conversation members carry the scope and history Pi's literal types fix.
func TestConversationSemanticsFixScopeAndHistory(t *testing.T) {
	if got := NewLatestConversationSemantics(ForkInitial).DocumentSemantics; got != (DocumentSemantics{Scope: ScopeConversation, History: HistoryLatest, Fork: ForkInitial}) {
		t.Fatalf("latest = %+v", got)
	}
	if got := NewRewindableConversationSemantics(ForkAsOf).DocumentSemantics; got != (DocumentSemantics{Scope: ScopeConversation, History: HistoryRewindable, Fork: ForkAsOf}) {
		t.Fatalf("rewindable = %+v", got)
	}
	// A constructed member is a definition DefineDoc admits; one with a fork its history cannot have is rejected.
	if singleton, family := defineWithSemantics(NewRewindableConversationSemantics(ForkAsOf).DocumentSemantics); singleton != "" || family != "" {
		t.Fatalf("rewindable asOf rejected: %q, %q", singleton, family)
	}
	if singleton, _ := defineWithSemantics(NewLatestConversationSemantics(ForkAsOf).DocumentSemantics); !strings.Contains(singleton, "semantics:") {
		t.Fatalf("latest asOf admitted: %q", singleton)
	}
}
