package ai

import (
	"runtime"
	"weak"
)

type assistantMessagePublication struct {
	cell    weak.Pointer[assistantMessageCell]
	cleanup runtime.Cleanup
}

type assistantPublications map[weak.Pointer[AssistantMessage]]*assistantMessagePublication

type assistantPublicationCleanup struct {
	stream  weak.Pointer[AssistantMessageEventStream]
	message weak.Pointer[AssistantMessage]
}

func cleanupAssistantPublication(cleanup assistantPublicationCleanup) {
	if stream := cleanup.stream.Value(); stream != nil {
		stream.mu.Lock()
		delete(stream.publications, cleanup.message)
		stream.mu.Unlock()
	}
}

// The registry preserves source-object identity without retaining consumed messages or mutating caller-owned public structs. Builders publish their own cells; forwarded views keep the original cell.
func (stream *AssistantMessageEventStream) publishPartialLocked(message *AssistantMessage) *AssistantMessage {
	if message == nil || message.observation != nil {
		return message
	}
	if stream.producer == nil {
		// pig divergence (D82): a producer outside the JavaScript-order executor publishes an immutable emission-time snapshot. A live view would expose whatever revision a free-running goroutine reached when a consumer happened to observe it.
		snapshot := cloneAssistantMessage(*message)
		return &snapshot
	}
	if stream.publications == nil {
		stream.publications = make(assistantPublications)
	}
	key := weak.Make(message)
	publication := stream.publications[key]
	if publication == nil {
		publication = &assistantMessagePublication{}
		publication.cleanup = runtime.AddCleanup(message, cleanupAssistantPublication, assistantPublicationCleanup{stream: weak.Make(stream), message: key})
		stream.publications[key] = publication
	}
	cell := publication.cell.Value()
	if cell == nil {
		cell = newAssistantMessageCell(message)
		publication.cell = weak.Make(cell)
	} else {
		cell.publish(message, assistantMessageReplacements{})
	}
	view := cell.view()
	runtime.KeepAlive(message)
	return view
}

func (stream *AssistantMessageEventStream) publishTerminalLocked(message *AssistantMessage) {
	if message == nil {
		return
	}
	if publication := stream.publications[weak.Make(message)]; publication != nil {
		if cell := publication.cell.Value(); cell != nil {
			cell.publish(message, assistantMessageReplacements{})
		}
	}
	runtime.KeepAlive(message)
}

func (stream *AssistantMessageEventStream) clearPublicationsLocked() {
	for _, publication := range stream.publications {
		publication.cleanup.Stop()
	}
	stream.publications = nil
}
