package transcript

import (
	"container/list"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// ReplyDisplay is what a plugin adds to its entry kind's messages.
type ReplyDisplay struct {
	ReplyTo *ReplyQuote
	Note    string
}

// EntryDisplayOf is what a plugin adds to its entry kind's messages (see `TranscriptEntries` in plugins/chat/contract.ts): nil adds nothing.
type EntryDisplayOf func(kind string, data any) *ReplyDisplay

// Go forced difference: upstream memoizes each entry's items in WeakMaps keyed by the (identical) entry objects so memoized React components
// skip rendering; items here are values rebuilt on every build, and a component that wants to skip compares them.

// seenCap is the newest SEEN_CALLS model-issued calls the nested-call memory keeps.
const seenCap = 1000

// seenCalls is the last nested calls this process saw for each model-issued call, by call ID.
var seenCalls = struct {
	mu    sync.Mutex
	calls map[string]*list.Element
	order *list.List
}{calls: map[string]*list.Element{}, order: list.New()}

type seenEntry struct {
	id    string
	calls []NestedCall
}

func rememberCalls(callID string, calls []NestedCall) {
	seenCalls.mu.Lock()
	defer seenCalls.mu.Unlock()
	if element, ok := seenCalls.calls[callID]; ok {
		seenCalls.order.Remove(element)
	}
	seenCalls.calls[callID] = seenCalls.order.PushBack(seenEntry{callID, calls})
	if seenCalls.order.Len() > seenCap {
		oldest := seenCalls.order.Front()
		seenCalls.order.Remove(oldest)
		delete(seenCalls.calls, oldest.Value.(seenEntry).id)
	}
}

func seenCallsOf(callID string) ([]NestedCall, bool) {
	seenCalls.mu.Lock()
	defer seenCalls.mu.Unlock()
	element, ok := seenCalls.calls[callID]
	if !ok {
		return nil, false
	}
	return element.Value.(seenEntry).calls, true
}

// nestedCalls are the nested calls of model-issued call callID, as Durable shows them. While the call runs that is `pi.live.nestedTools` (slots:
// this call's slots, parents before children); the commit appending its result removes them. A settled call shows the last live list this process
// saw (lost on reload); the tool's contribution may read a final list from its result instead (`ToolActivity.nestedCalls`). nil is `undefined`.
func nestedCalls(callID string, slots []harness.NestedToolSlot, settled bool) []NestedCall {
	if len(slots) > 0 {
		depths := map[string]int{}
		calls := make([]NestedCall, len(slots))
		for i, slot := range slots {
			depth := 0
			if parent, ok := depths[slot.ParentCallId]; ok {
				depth = parent + 1
			}
			depths[slot.CallId] = depth
			call := NestedCall{ID: slot.CallId, Name: slot.Name, Depth: depth}
			switch arguments := jsvalue.FromDelta(slot.Arguments).(type) {
			case *jsvalue.Object:
				call.Args = arguments
			case string:
				call.ArgsText = arguments
			default:
				call.Args = jsvalue.NewObject()
			}
			summary := slot.Summary
			// Done without a summary: the scheduler faulted or orphaned the call.
			switch {
			case slot.Status != harness.ToolSlotDone:
				call.Status = ToolStatus(slot.Status)
			case summary != nil && !summary.IsError:
				call.Status = ToolDone
			case summary != nil && runtime.IsInterruptedError(summary.Error):
				call.Status = ToolInterrupted
			default:
				call.Status = ToolError
			}
			if slot.Status == harness.ToolSlotRunning && slot.Output != nil && *slot.Output != "" {
				call.Output = *slot.Output
			}
			if summary != nil && summary.DurationMs != nil {
				duration := float64(*summary.DurationMs)
				call.DurationMs = &duration
			}
			switch {
			case summary != nil && summary.Error != "":
				call.Error = summary.Error
			case slot.Status == harness.ToolSlotDone && summary == nil:
				call.Error = "No result"
			}
			calls[i] = call
		}
		rememberCalls(callID, calls)
		return calls
	}
	seen, ok := seenCallsOf(callID)
	if !ok {
		return nil
	}
	if !settled {
		return seen
	}
	// A call that never finished before its caller settled was aborted with it.
	calls := make([]NestedCall, len(seen))
	for i, call := range seen {
		if call.Status == ToolRunning || call.Status == ToolPending {
			call.Status = ToolInterrupted
		}
		calls[i] = call
	}
	return calls
}

func userText(content ai.UserContent) string {
	switch typed := content.(type) {
	case ai.UserText:
		return string(typed)
	case ai.UserContentBlocks:
		parts := make([]string, 0, len(typed))
		for _, block := range typed {
			if text, ok := block.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func userImages(content ai.UserContent) []ai.ImageContent {
	var images []ai.ImageContent
	if blocks, ok := content.(ai.UserContentBlocks); ok {
		for _, block := range blocks {
			if image, ok := block.(ai.ImageContent); ok {
				images = append(images, image)
			}
		}
	}
	return images
}

func resultText(content []ai.ToolResultMessageContent) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// messageText is `text(message.content)` of any message.
func messageText(message ai.Message) string {
	switch typed := message.(type) {
	case ai.UserMessage:
		return userText(typed.Content)
	case ai.ToolResultMessage:
		return resultText(typed.Content)
	case ai.AssistantMessage:
		parts := make([]string, 0, len(typed.Content))
		for _, block := range typed.Content {
			if text, ok := block.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func resultTool(result ai.ToolResultMessage, name string) ToolView {
	if name == "" {
		name = result.ToolName
	}
	view := ToolView{
		ID:      result.ToolCallID,
		Name:    name,
		Args:    jsvalue.NewObject(),
		Status:  ToolDone,
		Output:  resultText(result.Content),
		Details: jsvalue.FromDelta(result.Details),
	}
	if result.IsError {
		view.Status = ToolError
	}
	for _, block := range result.Content {
		if image, ok := block.(ai.ImageContent); ok {
			view.Images = append(view.Images, image)
		}
	}
	if result.DurationMs != nil {
		duration := float64(*result.DurationMs)
		view.DurationMs = &duration
	}
	return view
}

// shellItems is a `p2.shell` entry as a marker: `$ command`, and the output with how it ended.
func shellItems(key string, raw any) []Item {
	data, ok := runtime.ShellDataOf(raw)
	if !ok {
		return nil
	}
	fence := strings.Repeat("`", max(3, longestBacktickRun(data.Output)+1))
	status := ""
	switch {
	case data.Cancelled:
		status = "Cancelled"
	case data.Error != nil && *data.Error != "":
		status = *data.Error
	case data.ExitCode != nil && *data.ExitCode != 0:
		status = "Exited with code " + jsnumber.String(*data.ExitCode)
	}
	detail := []string{"(no output)"}
	if data.Output != "" {
		detail[0] = fence + "\n" + data.Output + "\n" + fence
	}
	if data.Truncated {
		detail = append(detail, "Earlier output was dropped.")
	}
	if status != "" {
		detail = append(detail, status)
	}
	by := "the owner"
	if data.Author.Kind != runtime.AuthorOwner {
		by = data.Author.Name + " (guest)"
	}
	shared := ""
	if !data.IncludeInContext {
		shared = ", not shared with Pi"
	}
	detail = append(detail, "Run by "+by+shared+".")
	return []Item{MarkerItem{Key: key, Label: "$ " + data.Command, Detail: strings.Join(detail, "\n\n")}}
}

func longestBacktickRun(text string) int {
	longest, run := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// runFailureText is what a run that ended without an answer shows (`pi.live.runFailure`); ok is false for a Stop or a new context.
func runFailureText(failure harness.RunFailure) (string, bool) {
	detail := ""
	if failure.Detail != nil {
		detail = strings.TrimSpace(*failure.Detail)
	}
	switch failure.Reason {
	// The detail names the error, or the missing model ("Model faux/gone is not available").
	case harness.RunModelError, harness.RunNoModel:
		if detail != "" {
			return "The model could not answer this input. " + detail, true
		}
		return "The model could not answer this input.", true
	case harness.RunFaulted:
		if detail != "" {
			return "The run failed: " + detail, true
		}
		return "The run failed.", true
	case harness.RunOrphaned:
		if detail != "" {
			return "The run could not continue: " + detail, true
		}
		return "The run could not continue.", true
	case harness.RunInterrupted:
		return "The answer was interrupted: the server stopped while the model was answering.", true
	// The detail says what was cut off how often ("The generation was interrupted 5 times in its request phase").
	case harness.RunRestartLimit:
		if detail != "" {
			return "The run was stopped after repeated restarts. " + detail + ".", true
		}
		return "The run was stopped after repeated restarts.", true
	}
	return "", false
}

func gapKey(gap harness.TranscriptGap) string {
	return "gap-" + strconv.FormatInt(int64(gap.After), 10)
}

// BuildTranscript is the display items of a conversation's transcript (`TranscriptStore`): its entries in ID order with each gap, and the live
// state of the view's documents. People's messages come from entry data (src/runtime/input.ts); display lets a plugin add to its kinds' messages.
// filling names the gaps being loaded (their keys).
func BuildTranscript(view harness.TranscriptView, display EntryDisplayOf, filling map[string]bool) Transcript {
	live := doc[harness.LiveState](view.Docs, "pi.live")
	inbox := doc[harness.InboxState](view.Docs, "pi.inbox")
	var live0 harness.LiveState
	if live != nil {
		live0 = *live
	}
	compactionError := ""
	if live0.CompactionFailure != nil {
		compactionError = "Compaction failed: " + live0.CompactionFailure.Error
	}
	busy := live0.Run != nil

	results := map[string]ai.ToolResultMessage{}
	var latest *durable.EntryRecord
	for _, item := range view.Items {
		entry, ok := item.(harness.TranscriptEntry)
		if !ok || len(entry.Entry.Model) == 0 {
			continue
		}
		switch message := entry.Entry.Model[0].(type) {
		case ai.ToolResultMessage:
			results[message.ToolCallID] = message
		case ai.AssistantMessage:
			latest = &entry.Entry
		}
	}
	slots := map[string]harness.ToolSlot{}
	for _, slot := range live0.Tools {
		slots[slot.CallId] = slot
	}
	// Nested slots by the model-issued call they descend from; the list holds parents before their children.
	nested := map[string][]harness.NestedToolSlot{}
	rootOf := map[string]string{}
	for _, slot := range live0.NestedTools {
		root, ok := rootOf[slot.ParentCallId]
		if !ok {
			root = slot.ParentCallId
		}
		rootOf[slot.CallId] = root
		nested[root] = append(nested[root], slot)
	}
	called := map[string]bool{}
	// Keys of responses already placed: a second response started in the same millisecond keys by its entry.
	responseKeys := map[string]bool{}
	var items []Item

	displayOf := func(entry durable.EntryRecord) *ReplyDisplay {
		if display == nil {
			return nil
		}
		return display(entry.Kind, entry.Data)
	}
	// A person's message: who wrote it and what, from its data; a plugin may add a reply quote or a note.
	personItem := func(entry durable.EntryRecord, person runtime.MessageData, images []ai.ImageContent, chat bool, at *int64) Item {
		id := int(entry.Id)
		author := person.Author
		item := UserItem{
			Key: strconv.FormatInt(int64(entry.Id), 10), EntryID: &id, At: at, Images: images, Author: &author, Text: person.Text, Chat: chat,
			Files: make([]string, len(person.Attachments)),
		}
		for i, file := range person.Attachments {
			item.Files[i] = file.Name
		}
		if extra := displayOf(entry); extra != nil {
			item.ReplyTo, item.Note = extra.ReplyTo, extra.Note
		}
		return item
	}
	assistantItem := func(key string, message ai.AssistantMessage, calls []ai.ToolCall, current bool) AssistantItem {
		tools := make([]ToolView, len(calls))
		for i, part := range calls {
			result, hasResult := results[part.ID]
			// Live slots belong to the current tool round; an unfinished older call is interrupted.
			slot, hasSlot := slots[part.ID]
			callList := nestedCalls(part.ID, nested[part.ID], hasResult || !current || !busy)
			args := CallArguments(part)
			if hasResult {
				tool := resultTool(result, part.Name)
				tool.Args = args
				if hasSlot && slot.Output != nil && *slot.Output != "" && resultText(result.Content) == "" {
					tool.Output = *slot.Output
				}
				tool.Calls = callList
				tools[i] = tool
				continue
			}
			status := ToolInterrupted
			switch {
			case hasSlot && slot.Status == harness.ToolSlotDone:
				status = ToolError
			case hasSlot:
				status = ToolStatus(slot.Status)
			case busy && current:
				status = ToolPending
			}
			tool := ToolView{ID: part.ID, Name: part.Name, Args: args, Status: status, Calls: callList}
			if hasSlot {
				if slot.Output != nil {
					tool.Output = *slot.Output
				}
				if slot.Details != nil {
					tool.Details = jsvalue.FromDelta(*slot.Details)
				}
			}
			tools[i] = tool
		}
		return AssistantItem{Key: key, Message: &message, Tools: tools}
	}

	for _, transcriptItem := range view.Items {
		switch typed := transcriptItem.(type) {
		case harness.TranscriptEarlier:
			continue
		case harness.TranscriptGap:
			key := gapKey(typed)
			items = append(items, GapItem{Key: key, Gap: typed, Loading: filling[key]})
			continue
		}
		entry := transcriptItem.(harness.TranscriptEntry).Entry
		var message ai.Message
		if len(entry.Model) > 0 {
			message = entry.Model[0]
		}
		key := strconv.FormatInt(int64(entry.Id), 10)
		id := int(entry.Id)
		switch {
		case entry.Kind == "app.model-error":
			// Notices earlier p2 versions wrote for inputs the model could not answer; `runFailure` replaced them.
			detail := ""
			if data := jsvalue.FromDelta(entry.Data); data != nil {
				if object, ok := data.(*jsvalue.Object); ok {
					if text, ok := object.Lookup("detail").(string); ok {
						detail = text
					}
				}
			}
			text := "The model could not answer this input."
			if strings.TrimSpace(detail) != "" {
				text += " " + detail
			}
			items = append(items, FailureItem{Key: key, Text: text})
		case entry.Kind == "pi.compaction":
			detail := ""
			if message != nil {
				detail = messageText(message)
			}
			items = append(items, MarkerItem{Key: key, Label: "Context compacted", Detail: detail})
		case entry.Kind == "pi.reset":
			marker := MarkerItem{Key: key, Label: "New context"}
			if user, ok := message.(ai.UserMessage); ok {
				marker.Detail = userText(user.Content)
			}
			items = append(items, marker)
		case entry.Kind == runtime.ShellEntry:
			// A user shell command (`!cmd`/`!!cmd`): read-only, its output in a fenced block.
			items = append(items, shellItems(key, entry.Data)...)
		default:
			switch typed := message.(type) {
			case ai.UserMessage:
				images := userImages(typed.Content)
				// A person's input from its data.
				at := typed.Timestamp
				if person, ok := runtime.MessageDataOf(entry.Data); ok {
					items = append(items, personItem(entry, person, images, false, &at))
				} else if fromAgent, ok := runtime.AgentMessageOf(entry.Kind, entry.Data); ok {
					items = append(items, UserItem{Key: key, EntryID: &id, At: &at, Images: images, Agent: &AgentRef{From: fromAgent.FromName, Intent: string(fromAgent.Intent)}, Text: fromAgent.Text, Files: []string{}})
				} else if peer, ok := runtime.PeerMessageOf(entry.Kind, entry.Data); ok {
					// A peer session's message from its data, or a tool's plain input.
					items = append(items, UserItem{Key: key, EntryID: &id, At: &at, Images: images, Peer: &ConversationPeer{ID: peer.FromID, Title: peer.FromTitle}, Text: peer.Text, Files: []string{}})
				} else {
					items = append(items, UserItem{Key: key, EntryID: &id, At: &at, Images: images, Text: userText(typed.Content), Files: []string{}})
				}
			case ai.AssistantMessage:
				// Keep empty abnormal responses visible; context filtering is not a display filter. Calls of the newest response without a result or
				// slot are pending while busy; older ones were interrupted.
				current := latest != nil && latest.Id == entry.Id
				var calls []ai.ToolCall
				for _, block := range typed.Content {
					if call, ok := block.(ai.ToolCall); ok {
						calls = append(calls, call)
						called[call.ID] = true
					}
				}
				item := assistantItem("response-"+strconv.FormatInt(typed.Timestamp, 10), typed, calls, current)
				item.EntryID = &id
				if responseKeys[item.Key] {
					item.Key = key
				}
				responseKeys[item.Key] = true
				items = append(items, item)
			case ai.ToolResultMessage:
				if !called[typed.ToolCallID] {
					items = append(items, ToolItem{Key: key, Tool: resultTool(typed, "")})
				}
			case nil:
				if person, ok := runtime.MessageDataOf(entry.Data); ok {
					items = append(items, personItem(entry, person, nil, true, nil))
				}
			}
		}
	}
	if live0.Generation != nil && live0.Generation.Message != nil {
		partial := *live0.Generation.Message
		item := AssistantItem{Key: "response-" + strconv.FormatInt(partial.Timestamp, 10), Message: &partial, Tools: []ToolView{}, Streaming: true}
		if responseKeys[item.Key] {
			item.Key = "live"
		}
		items = append(items, item)
	}
	if failure := live0.CompactionFailure; failure != nil {
		// State-derived, not a retained entry: it persists while idle and disappears when Durable clears it. Both the browser and p2 TUI render
		// failure items in their existing error-notice style.
		items = append(items, FailureItem{Key: fmt.Sprintf("compaction-failure-%d", failure.TaskId), Text: "Compaction failed: " + failure.Error})
	}
	// Why the last run ended without an answer, until the next run starts; a blocking compaction failure is shown once.
	if failure := live0.RunFailure; failure != nil && !(compactionError != "" && failure.Detail != nil && *failure.Detail == compactionError) {
		if text, ok := runFailureText(*failure); ok {
			items = append(items, FailureItem{Key: fmt.Sprintf("run-failure-%d", failure.TaskId), Text: text})
		}
	}

	result := Transcript{
		Items: items,
		Busy:  busy,
		// Keep one stable activity signal through thinking, text, tools and compaction. It disappears only when the viewed conversation is actually idle.
		Working: busy || len(live0.Compactions) > 0,
		Agent:   harness.AgentState{},
	}
	if len(view.Items) > 0 {
		_, result.Earlier = view.Items[0].(harness.TranscriptEarlier)
	}
	if artifacts := artifactsOf(view.Docs); len(artifacts) > 0 {
		result.Artifacts = artifacts
	}
	if live0.Generation != nil && live0.Generation.Retry != nil {
		result.Retry = &RetryInfo{At: int64(live0.Generation.Retry.At), Error: live0.Generation.Retry.Error}
	}
	for _, status := range live0.Compactions {
		compaction := Compaction{ID: int(status.TaskId), Trigger: string(status.Trigger), Blocking: status.Blocking, Attempt: status.Attempt}
		if status.Retry != nil {
			compaction.Retry = &RetryInfo{At: int64(status.Retry.At), Error: status.Retry.Error}
		}
		result.Compactions = append(result.Compactions, compaction)
	}
	if inbox != nil {
		for _, item := range inbox.Items {
			if item.Mode == harness.InboxWrite {
				continue
			}
			text := ""
			switch content := item.Content.(type) {
			case string:
				text = content
			case []any:
				parts := make([]string, 0, len(content))
				for _, part := range content {
					if block, ok := part.(durable.JsonObject); ok && block != nil && block.Value("type") == "text" {
						value, _ := block.Value("text").(string)
						parts = append(parts, value)
					}
				}
				text = strings.Join(parts, "\n")
			}
			if item.Entry != nil {
				entryData := item.Entry.Value("data")
				if person, ok := runtime.MessageDataOf(entryData); ok {
					text = person.Text
				} else if kind, _ := item.Entry.Value("kind").(string); kind != "" {
					if peer, ok := runtime.PeerMessageOf(kind, entryData); ok {
						text = peer.Text
					}
				}
			}
			result.Queued = append(result.Queued, Queued{ID: int(item.Id), Mode: string(item.Mode), Text: text})
		}
	}
	if agent := doc[harness.AgentState](view.Docs, "pi.agent"); agent != nil {
		result.Agent = *agent
	}
	return result
}

// doc decodes the document of kind; nil when absent.
func doc[T any](docs harness.ViewDocs, kind string) *T {
	value, ok := docs.Get(kind)
	if !ok {
		return nil
	}
	typed, err := durable.DecodeDoc[T](value)
	if err != nil {
		return nil
	}
	return typed
}

// artifactsOf are the artifact versions `p2.artifacts` lists, newest first; entries that are not artifact descriptions are left out.
func artifactsOf(docs harness.ViewDocs) []runtime.ArtifactInfo {
	state, ok := docs.Get("p2.artifacts")
	if !ok || state == nil {
		return nil
	}
	list, _ := state.Value("artifacts").([]any)
	var infos []runtime.ArtifactInfo
	for i := len(list) - 1; i >= 0; i-- {
		if info, ok := runtime.ArtifactInfoOf(list[i]); ok {
			infos = append(infos, info)
		}
	}
	return infos
}

// CallArguments are a tool call's arguments as the cards read them: an object in the member order the model sent (an empty one when the call has none).
func CallArguments(call ai.ToolCall) *jsvalue.Object {
	text, err := call.ArgumentsJSON()
	if err != nil {
		return jsvalue.NewObject()
	}
	value, err := jsvalue.Parse([]byte(text))
	if err != nil {
		return jsvalue.NewObject()
	}
	if object, ok := value.(*jsvalue.Object); ok {
		return object
	}
	return jsvalue.NewObject()
}
