package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// This file translates the pure, synchronous parts of @smithy/core 3.35.1 event streams (their modules are unchanged from 3.33.3) that Pi's Bedrock provider reads through the AWS SDK: getChunkedStream's framing (eventstream-serde-universal/getChunkedStream.js), EventStreamCodec.decode (eventstream-codec/EventStreamCodec.js, splitMessage.js, HeaderMarshaller.js parse) and the message unmarshaller (eventstream-serde-universal/getUnmarshalledStream.js getMessageUnmarshaller, EventStreamSerde.js deserializeEventStream). The awaits between these steps are modeled by bedrock_stream_pipeline.go.

const (
	bedrockPreludeLength     = 8
	bedrockChecksumLength    = 4
	bedrockMinimumMessageLen = bedrockPreludeLength + 2*bedrockChecksumLength
	// bedrockLengthPrefix is the big-endian total length that opens every message; getChunkedStream buffers it before it allocates the message.
	bedrockLengthPrefix = 4
	// bedrockMessageAllocationHint bounds the up-front allocation for a reported length; the message grows as its bytes arrive, so a corrupt length cannot reserve gigabytes (Node allocates `new Uint8Array(size)` at once).
	bedrockMessageAllocationHint = 1 << 20
)

// bedrockMessageChunker is getChunkedStream's state: it assembles complete messages from arbitrary chunk boundaries.
type bedrockMessageChunker struct {
	total     int
	pending   int
	message   []byte
	hasMsg    bool
	lengthBuf []byte
}

// feed is the body of getChunkedStream's inner loop for one source value (chunk). It returns every message the chunk completes, in yield order. An allocation failure surfaces after the messages yielded before it.
func (chunker *bedrockMessageChunker) feed(chunk []byte) (messages [][]byte, err error) {
	offset := 0
	for offset < len(chunk) {
		if !chunker.hasMsg {
			remaining := len(chunk) - offset
			if chunker.lengthBuf == nil {
				chunker.lengthBuf = make([]byte, bedrockLengthPrefix)
			}
			forTotal := min(bedrockLengthPrefix-chunker.pending, remaining)
			copy(chunker.lengthBuf[chunker.pending:], chunk[offset:offset+forTotal])
			chunker.pending += forTotal
			offset += forTotal
			if chunker.pending < bedrockLengthPrefix {
				break
			}
			size := int(binary.BigEndian.Uint32(chunker.lengthBuf))
			chunker.lengthBuf = nil
			if size < bedrockLengthPrefix {
				// DataView.setUint32(0, size) on a Uint8Array shorter than four bytes.
				chunker.total, chunker.pending = size, 0
				return messages, errors.New("Offset is outside the bounds of the DataView")
			}
			chunker.total = size
			chunker.pending = bedrockLengthPrefix
			chunker.message = make([]byte, bedrockLengthPrefix, min(size, bedrockMessageAllocationHint))
			binary.BigEndian.PutUint32(chunker.message, uint32(size))
			chunker.hasMsg = true
		}
		write := min(chunker.total-chunker.pending, len(chunk)-offset)
		chunker.message = append(chunker.message, chunk[offset:offset+write]...)
		chunker.pending += write
		offset += write
		if chunker.total != 0 && chunker.total == chunker.pending {
			messages = append(messages, chunker.message)
			chunker.message, chunker.hasMsg = nil, false
			chunker.total, chunker.pending = 0, 0
		}
	}
	return messages, nil
}

// finish is getChunkedStream's `done` branch.
func (chunker *bedrockMessageChunker) finish() error {
	if chunker.total == 0 {
		return nil
	}
	return errors.New("Truncated event message received.")
}

type bedrockEventHeader struct {
	kind  string
	value string
}

type bedrockEventMessage struct {
	headers map[string]bedrockEventHeader
	body    []byte
}

// decodeBedrockEventMessage is EventStreamCodec.decode: splitMessage validates length and both checksums, then HeaderMarshaller.parse reads the headers.
func decodeBedrockEventMessage(message []byte) (*bedrockEventMessage, error) {
	if len(message) < bedrockMinimumMessageLen {
		return nil, errors.New("Provided message too short to accommodate event stream message overhead")
	}
	messageLength := binary.BigEndian.Uint32(message)
	if uint64(len(message)) != uint64(messageLength) {
		return nil, errors.New("Reported message length does not match received message length")
	}
	headerLength := int(binary.BigEndian.Uint32(message[4:]))
	expectedPrelude := binary.BigEndian.Uint32(message[8:])
	expectedMessage := binary.BigEndian.Uint32(message[len(message)-bedrockChecksumLength:])
	prelude := crc32.ChecksumIEEE(message[:bedrockPreludeLength])
	if expectedPrelude != prelude {
		return nil, fmt.Errorf("The prelude checksum specified in the message (%d) does not match the calculated CRC32 checksum (%d)", expectedPrelude, prelude)
	}
	full := crc32.Update(prelude, crc32.IEEETable, message[bedrockPreludeLength:len(message)-bedrockChecksumLength])
	if expectedMessage != full {
		return nil, fmt.Errorf("The message checksum (%d) did not match the expected value of %d", full, expectedMessage)
	}
	headersStart := bedrockPreludeLength + bedrockChecksumLength
	bodyStart := headersStart + headerLength
	bodyEnd := len(message) - bedrockChecksumLength
	if headerLength < 0 || bodyStart > bodyEnd {
		return nil, errors.New("Offset is outside the bounds of the DataView")
	}
	headers, err := parseBedrockEventHeaders(message[headersStart:bodyStart])
	if err != nil {
		return nil, err
	}
	return &bedrockEventMessage{headers: headers, body: message[bodyStart:bodyEnd]}, nil
}

var errBedrockHeaderBounds = errors.New("Offset is outside the bounds of the DataView")

// parseBedrockEventHeaders is HeaderMarshaller.parse. Only string values are retained; the message and event types are strings.
func parseBedrockEventHeaders(headers []byte) (map[string]bedrockEventHeader, error) {
	out := map[string]bedrockEventHeader{}
	position := 0
	need := func(count int) bool { return count >= 0 && position+count <= len(headers) }
	for position < len(headers) {
		nameLength := int(headers[position])
		position++
		if !need(nameLength) {
			return nil, errBedrockHeaderBounds
		}
		name := strings.ToValidUTF8(string(headers[position:position+nameLength]), "\ufffd")
		position += nameLength
		if !need(1) {
			return nil, errBedrockHeaderBounds
		}
		tag := headers[position]
		position++
		switch tag {
		case 0:
			out[name] = bedrockEventHeader{kind: "boolean", value: "true"}
		case 1:
			out[name] = bedrockEventHeader{kind: "boolean", value: "false"}
		case 2:
			if !need(1) {
				return nil, errBedrockHeaderBounds
			}
			out[name] = bedrockEventHeader{kind: "byte", value: fmt.Sprint(int8(headers[position]))}
			position++
		case 3:
			if !need(2) {
				return nil, errBedrockHeaderBounds
			}
			out[name] = bedrockEventHeader{kind: "short", value: fmt.Sprint(int16(binary.BigEndian.Uint16(headers[position:])))}
			position += 2
		case 4:
			if !need(4) {
				return nil, errBedrockHeaderBounds
			}
			out[name] = bedrockEventHeader{kind: "integer", value: fmt.Sprint(int32(binary.BigEndian.Uint32(headers[position:])))}
			position += 4
		case 5, 8:
			if !need(8) {
				return nil, errBedrockHeaderBounds
			}
			kind := "long"
			if tag == 8 {
				kind = "timestamp"
			}
			out[name] = bedrockEventHeader{kind: kind, value: fmt.Sprint(int64(binary.BigEndian.Uint64(headers[position:])))}
			position += 8
		case 6, 7:
			if !need(2) {
				return nil, errBedrockHeaderBounds
			}
			length := int(binary.BigEndian.Uint16(headers[position:]))
			position += 2
			if !need(length) {
				return nil, errBedrockHeaderBounds
			}
			kind := "binary"
			if tag == 7 {
				kind = "string"
			}
			out[name] = bedrockEventHeader{kind: kind, value: strings.ToValidUTF8(string(headers[position:position+length]), "\ufffd")}
			position += length
		case 9:
			if !need(16) {
				return nil, errBedrockHeaderBounds
			}
			b := headers[position : position+16]
			out[name] = bedrockEventHeader{kind: "uuid", value: fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])}
			position += 16
		default:
			return nil, errors.New("Unrecognized header type tag")
		}
	}
	return out, nil
}

// bedrockStreamItem is one deserialized ConverseStream event. The SDK's union types carry one member; Pi's handler reads every reasoningContent member a payload holds, so a reasoning delta also carries all of its members.
type bedrockStreamItem struct {
	event     btypes.ConverseStreamOutput
	reasoning *bedrockReasoningFields
}

// bedrockReasoningFields are the members of a reasoningContent delta. A pointer distinguishes an absent member from an empty one.
type bedrockReasoningFields struct {
	text      *string
	signature *string
	redacted  []byte
}

// bedrockEventDeserializer is getMessageUnmarshaller composed with EventStreamSerde.deserializeEventStream's per-event function. A nil event without an error is an event the ConverseStream union does not model: the SDK's `$unknown` result, which SmithyMessageDecoderStream drops.
func bedrockEventDeserializer(message *bedrockEventMessage) (bedrockStreamItem, error) {
	messageType, ok := message.headers[":message-type"]
	if !ok {
		return bedrockStreamItem{}, errors.New("Cannot destructure property 'value' of 'message.headers.:message-type' as it is undefined.")
	}
	switch messageType.value {
	case "error":
		text := message.headers[":error-message"].value
		if text == "" {
			text = "UnknownError"
		}
		return bedrockStreamItem{}, &smithy.GenericAPIError{Code: message.headers[":error-code"].value, Message: text}
	case "exception":
		code := message.headers[":exception-type"].value
		exception := bedrockStreamException(code, message.body)
		if exception == nil {
			return bedrockStreamItem{}, &smithy.GenericAPIError{Code: code, Message: strings.ToValidUTF8(string(message.body), "\ufffd")}
		}
		return bedrockStreamItem{}, exception
	case "event":
		return bedrockStreamEvent(message.headers[":event-type"].value, message.body)
	default:
		return bedrockStreamItem{}, fmt.Errorf("Unrecognizable event type: %s", message.headers[":event-type"].value)
	}
}

type bedrockStreamExceptionBody struct {
	Message            *string `json:"message"`
	OriginalStatusCode *int32  `json:"originalStatusCode"`
	OriginalMessage    *string `json:"originalMessage"`
}

// bedrockStreamException maps a modeled ConverseStream exception member to its SDK error. It returns nil for a code the union does not model.
func bedrockStreamException(code string, body []byte) error {
	var fields bedrockStreamExceptionBody
	_ = json.Unmarshal(body, &fields)
	message := fields.Message
	if message == nil {
		message = aws.String("Unknown")
	}
	switch code {
	case "internalServerException":
		return &btypes.InternalServerException{Message: message}
	case "modelStreamErrorException":
		return &btypes.ModelStreamErrorException{Message: message, OriginalStatusCode: fields.OriginalStatusCode, OriginalMessage: fields.OriginalMessage}
	case "validationException":
		return &btypes.ValidationException{Message: message}
	case "throttlingException":
		return &btypes.ThrottlingException{Message: message}
	case "serviceUnavailableException":
		return &btypes.ServiceUnavailableException{Message: message}
	}
	return nil
}

type bedrockStreamDelta struct {
	Text    *string `json:"text"`
	ToolUse *struct {
		Input *string `json:"input"`
	} `json:"toolUse"`
	ReasoningContent *struct {
		Text            *string `json:"text"`
		Signature       *string `json:"signature"`
		RedactedContent *string `json:"redactedContent"`
	} `json:"reasoningContent"`
}

type bedrockStreamEventBody struct {
	// AdditionalModelResponseFields is the messageStop member the SDK reads as a document.
	AdditionalModelResponseFields any    `json:"additionalModelResponseFields"`
	Role                          string `json:"role"`
	ContentBlockIndex             *int32 `json:"contentBlockIndex"`
	Start                         *struct {
		ToolUse *struct {
			ToolUseID *string `json:"toolUseId"`
			Name      *string `json:"name"`
			Type      string  `json:"type"`
		} `json:"toolUse"`
	} `json:"start"`
	Delta      *bedrockStreamDelta `json:"delta"`
	StopReason string              `json:"stopReason"`
	Usage      *struct {
		InputTokens           *int32 `json:"inputTokens"`
		OutputTokens          *int32 `json:"outputTokens"`
		TotalTokens           *int32 `json:"totalTokens"`
		CacheReadInputTokens  *int32 `json:"cacheReadInputTokens"`
		CacheWriteInputTokens *int32 `json:"cacheWriteInputTokens"`
		CacheDetails          []struct {
			TTL         string `json:"ttl"`
			InputTokens *int32 `json:"inputTokens"`
		} `json:"cacheDetails"`
	} `json:"usage"`
}

// bedrockStreamEvent is the schema-driven read of one event payload (JsonShapeDeserializer2.read, then _read over the event member's schema).
func bedrockStreamEvent(eventType string, body []byte) (bedrockStreamItem, error) {
	var fields bedrockStreamEventBody
	if len(body) != 0 {
		// JsonShapeDeserializer2.read: JSON.parse(buffer) (@aws-sdk/core protocols/index.js:521).
		if err := jsonparse.Validate(body); err != nil {
			return bedrockStreamItem{}, err
		}
		if err := json.Unmarshal(body, &fields); err != nil {
			return bedrockStreamItem{}, err
		}
	}
	switch eventType {
	case "internalServerException", "modelStreamErrorException", "validationException", "throttlingException", "serviceUnavailableException":
		// Pi's union models the exception members, so an event frame carrying one is yielded as `{ <member>: exception }` (bedrock-converse-stream.ts:318-327). The Go union does not model them; the SDK reader yields *types.UnknownUnionMember for such a frame, and so does this decoder.
		return bedrockStreamItem{event: &btypes.UnknownUnionMember{Tag: eventType, Value: bytes.Clone(body)}}, nil
	case "messageStart":
		return bedrockStreamItem{event: &btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRole(fields.Role)}}}, nil
	case "contentBlockStart":
		event := btypes.ContentBlockStartEvent{ContentBlockIndex: fields.ContentBlockIndex}
		if fields.Start != nil && fields.Start.ToolUse != nil {
			event.Start = &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{
				ToolUseId: fields.Start.ToolUse.ToolUseID, Name: fields.Start.ToolUse.Name, Type: btypes.ToolUseType(fields.Start.ToolUse.Type),
			}}
		}
		return bedrockStreamItem{event: &btypes.ConverseStreamOutputMemberContentBlockStart{Value: event}}, nil
	case "contentBlockDelta":
		event := btypes.ContentBlockDeltaEvent{ContentBlockIndex: fields.ContentBlockIndex}
		item := bedrockStreamItem{}
		if delta := fields.Delta; delta != nil {
			switch {
			case delta.Text != nil:
				event.Delta = &btypes.ContentBlockDeltaMemberText{Value: *delta.Text}
			case delta.ToolUse != nil:
				event.Delta = &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: delta.ToolUse.Input}}
			case delta.ReasoningContent != nil:
				reasoning := &bedrockReasoningFields{text: delta.ReasoningContent.Text, signature: delta.ReasoningContent.Signature}
				if delta.ReasoningContent.RedactedContent != nil {
					decoded, err := decodeBedrockBlob(*delta.ReasoningContent.RedactedContent)
					if err != nil {
						return bedrockStreamItem{}, err
					}
					reasoning.redacted = decoded
				}
				event.Delta = &btypes.ContentBlockDeltaMemberReasoningContent{}
				item.reasoning = reasoning
			}
		}
		item.event = &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: event}
		return item, nil
	case "contentBlockStop":
		return bedrockStreamItem{event: &btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: fields.ContentBlockIndex}}}, nil
	case "messageStop":
		stop := btypes.MessageStopEvent{StopReason: btypes.StopReason(fields.StopReason)}
		if fields.AdditionalModelResponseFields != nil {
			stop.AdditionalModelResponseFields = document.NewLazyDocument(fields.AdditionalModelResponseFields)
		}
		return bedrockStreamItem{event: &btypes.ConverseStreamOutputMemberMessageStop{Value: stop}}, nil
	case "metadata":
		event := btypes.ConverseStreamMetadataEvent{}
		if usage := fields.Usage; usage != nil {
			event.Usage = &btypes.TokenUsage{
				InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
				CacheReadInputTokens: usage.CacheReadInputTokens, CacheWriteInputTokens: usage.CacheWriteInputTokens,
			}
			for _, detail := range usage.CacheDetails {
				event.Usage.CacheDetails = append(event.Usage.CacheDetails, btypes.CacheDetail{Ttl: btypes.CacheTTL(detail.TTL), InputTokens: detail.InputTokens})
			}
		}
		return bedrockStreamItem{event: &btypes.ConverseStreamOutputMemberMetadata{Value: event}}, nil
	}
	return bedrockStreamItem{}, nil
}

// decodeBedrockBlob is @smithy/core/serde fromBase64 for a blob member.
func decodeBedrockBlob(value string) ([]byte, error) {
	if (len(value)*3)%4 != 0 {
		return nil, errors.New("Incorrect padding on base64 string.")
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("Invalid base64 string.")
	}
	return decoded, nil
}
