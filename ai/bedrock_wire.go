//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"

	"github.com/aws/smithy-go"
	smithydocument "github.com/aws/smithy-go/document"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
	bdoc "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/document"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi's @aws-sdk/client-bedrock-runtime serializes a ConverseStream command from its schema (dist-cjs ConverseStreamRequest$ and the
// shapes below it): every structure writes its members in schema order, a union writes its one member, a blob is base64, and a document
// is JSON.stringify of the value Pi built, so its members keep their order and an empty member name stays. The Go SDK writes structure
// members sorted, and its document encoder sorts map keys and rejects an empty one, so PiG writes the body itself.

// bedrockDocument is a document whose JSON is fixed when it is made, in the member order of the value it stands for. It keeps the SDK's
// lazy document for the value, so a caller can still decode it.
type bedrockDocument struct {
	bdoc.Interface
	json []byte
}

// MarshalSmithyDocument returns the document's JSON.
func (document *bedrockDocument) MarshalSmithyDocument() ([]byte, error) {
	return slices.Clone(document.json), nil
}

// newBedrockDocument makes the document for value, whose JSON.stringify text is encoded.
func newBedrockDocument(value any, encoded []byte) bdoc.Interface {
	return &bedrockDocument{Interface: bdoc.NewLazyDocument(value), json: jsstring.UnescapeForStringify(encoded)}
}

// withBedrockConverseBody replaces the body the Go SDK serialized with the one Pi's SDK writes for the same input. metadataOrder is the
// insertion order of the requestMetadata keys.
func withBedrockConverseBody(metadataOrder []string) func(*smithymiddleware.Stack) error {
	return func(stack *smithymiddleware.Stack) error { return insertBedrockConverseBody(stack, metadataOrder) }
}

func insertBedrockConverseBody(stack *smithymiddleware.Stack, metadataOrder []string) error {
	return stack.Serialize.Insert(smithymiddleware.SerializeMiddlewareFunc("PiGConverseStreamBody", func(ctx context.Context, in smithymiddleware.SerializeInput, next smithymiddleware.SerializeHandler) (smithymiddleware.SerializeOutput, smithymiddleware.Metadata, error) {
		request, ok := in.Request.(*smithyhttp.Request)
		input, isInput := in.Parameters.(*bedrockruntime.ConverseStreamInput)
		if !ok || !isInput {
			return next.HandleSerialize(ctx, in)
		}
		body, err := appendBedrockConverseStreamBody(nil, input, metadataOrder)
		if err != nil {
			return smithymiddleware.SerializeOutput{}, smithymiddleware.Metadata{}, &smithy.SerializationError{Err: err}
		}
		if request, err = request.SetStream(bytes.NewReader(body)); err != nil {
			return smithymiddleware.SerializeOutput{}, smithymiddleware.Metadata{}, &smithy.SerializationError{Err: err}
		}
		in.Request = request
		return next.HandleSerialize(ctx, in)
	}), "OperationSerializer", smithymiddleware.After)
}

// bedrockWire writes JSON in JSON.stringify's form. The first error stops the writing.
type bedrockWire struct {
	buf []byte
	err error
}

// bedrockObject writes the members of one object, separating them.
type bedrockObject struct {
	wire    *bedrockWire
	members int
}

func (wire *bedrockWire) object() bedrockObject {
	wire.buf = append(wire.buf, '{')
	return bedrockObject{wire: wire}
}

func (object *bedrockObject) key(name string) *bedrockWire {
	if object.members > 0 {
		object.wire.buf = append(object.wire.buf, ',')
	}
	object.members++
	object.wire.buf = jsstring.AppendString(object.wire.buf, name)
	object.wire.buf = append(object.wire.buf, ':')
	return object.wire
}

func (object *bedrockObject) close() { object.wire.buf = append(object.wire.buf, '}') }

func (object *bedrockObject) str(name string, value *string) {
	if value != nil {
		object.key(name).str(*value)
	}
}

// enum writes a member whose zero value means unset.
func (object *bedrockObject) enum(name, value string) {
	if value != "" {
		object.key(name).str(value)
	}
}

func (object *bedrockObject) integer(name string, value *int32) {
	if value != nil {
		wire := object.key(name)
		wire.buf = strconv.AppendInt(wire.buf, int64(*value), 10)
	}
}

func (object *bedrockObject) float(name string, value *float32) {
	if value != nil {
		object.key(name).float32(*value)
	}
}

func (object *bedrockObject) boolean(name string, value *bool) {
	if value != nil {
		wire := object.key(name)
		wire.buf = strconv.AppendBool(wire.buf, *value)
	}
}

func (object *bedrockObject) blob(name string, value []byte) {
	if value != nil {
		wire := object.key(name)
		wire.buf = append(wire.buf, '"')
		wire.buf = base64.StdEncoding.AppendEncode(wire.buf, value)
		wire.buf = append(wire.buf, '"')
	}
}

func (object *bedrockObject) strings(name string, values []string) {
	if values != nil {
		bedrockList(object.key(name), values, (*bedrockWire).str)
	}
}

func (object *bedrockObject) document(name string, value smithydocument.Marshaler) {
	if value != nil {
		object.key(name).document(value)
	}
}

func (wire *bedrockWire) str(value string) { wire.buf = jsstring.AppendString(wire.buf, value) }

// float32 writes the Number the value stands for: its shortest float32 decimal, as the SDK's own serializer writes it.
func (wire *bedrockWire) float32(value float32) {
	number, _ := strconv.ParseFloat(strconv.FormatFloat(float64(value), 'g', -1, 32), 64)
	wire.buf = append(wire.buf, jsnumber.JSON(number)...)
}

func (wire *bedrockWire) document(value smithydocument.Marshaler) {
	if document, ok := value.(*bedrockDocument); ok {
		wire.buf = append(wire.buf, document.json...)
		return
	}
	encoded, err := value.MarshalSmithyDocument()
	if err != nil {
		wire.fail(err)
		return
	}
	wire.buf = append(wire.buf, jsstring.UnescapeForStringify(encoded)...)
}

func (wire *bedrockWire) fail(err error) {
	if wire.err == nil {
		wire.err = err
	}
}

func (wire *bedrockWire) unknown(union string, value any) {
	wire.fail(fmt.Errorf("attempted to serialize unknown member type %T for union %s", value, union))
	wire.buf = append(wire.buf, "null"...)
}

func bedrockList[T any](wire *bedrockWire, values []T, write func(*bedrockWire, T)) {
	wire.buf = append(wire.buf, '[')
	for i, value := range values {
		if i > 0 {
			wire.buf = append(wire.buf, ',')
		}
		write(wire, value)
	}
	wire.buf = append(wire.buf, ']')
}

// bedrockStringMap writes a map in the order a JavaScript object enumerates its keys when they were inserted in order and then the
// remaining keys sorted (a Go map keeps no insertion order).
func bedrockStringMap[T any](wire *bedrockWire, values map[string]T, order []string, write func(*bedrockWire, T)) {
	keys := make([]string, 0, len(values))
	for _, key := range order {
		if _, ok := values[key]; ok && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	ordered := len(keys)
	for key := range values {
		if !slices.Contains(keys[:ordered], key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys[ordered:])
	jsstring.SortOwnKeys(keys, func(key string) string { return key })
	object := wire.object()
	for _, key := range keys {
		write(object.key(key), values[key])
	}
	object.close()
}

// appendBedrockConverseStreamBody appends the body of a ConverseStream request in ConverseStreamRequest$ member order. modelId is in
// the path.
func appendBedrockConverseStreamBody(dst []byte, input *bedrockruntime.ConverseStreamInput, metadataOrder []string) ([]byte, error) {
	wire := &bedrockWire{buf: dst}
	object := wire.object()
	if input.Messages != nil {
		bedrockList(object.key("messages"), input.Messages, (*bedrockWire).message)
	}
	if input.System != nil {
		bedrockList(object.key("system"), input.System, (*bedrockWire).systemContentBlock)
	}
	if input.InferenceConfig != nil {
		object.key("inferenceConfig").inferenceConfiguration(input.InferenceConfig)
	}
	if input.ToolConfig != nil {
		object.key("toolConfig").toolConfiguration(input.ToolConfig)
	}
	if input.GuardrailConfig != nil {
		object.key("guardrailConfig").guardrailStreamConfiguration(input.GuardrailConfig)
	}
	object.document("additionalModelRequestFields", input.AdditionalModelRequestFields)
	if input.PromptVariables != nil {
		bedrockStringMap(object.key("promptVariables"), input.PromptVariables, nil, (*bedrockWire).promptVariableValues)
	}
	object.strings("additionalModelResponseFieldPaths", input.AdditionalModelResponseFieldPaths)
	if input.RequestMetadata != nil {
		bedrockStringMap(object.key("requestMetadata"), input.RequestMetadata, metadataOrder, (*bedrockWire).str)
	}
	if input.PerformanceConfig != nil {
		config := object.key("performanceConfig").object()
		config.enum("latency", string(input.PerformanceConfig.Latency))
		config.close()
	}
	if input.ServiceTier != nil {
		tier := object.key("serviceTier").object()
		tier.enum("type", string(input.ServiceTier.Type))
		tier.close()
	}
	if input.OutputConfig != nil {
		object.key("outputConfig").outputConfig(input.OutputConfig)
	}
	object.close()
	return wire.buf, wire.err
}

func (wire *bedrockWire) message(message btypes.Message) {
	object := wire.object()
	object.enum("role", string(message.Role))
	if message.Content != nil {
		bedrockList(object.key("content"), message.Content, (*bedrockWire).contentBlock)
	}
	object.close()
}

func (wire *bedrockWire) contentBlock(block btypes.ContentBlock) {
	object := wire.object()
	switch block := block.(type) {
	case *btypes.ContentBlockMemberText:
		object.key("text").str(block.Value)
	case *btypes.ContentBlockMemberImage:
		object.key("image").imageBlock(&block.Value)
	case *btypes.ContentBlockMemberDocument:
		object.key("document").documentBlock(&block.Value)
	case *btypes.ContentBlockMemberVideo:
		object.key("video").videoBlock(&block.Value)
	case *btypes.ContentBlockMemberAudio:
		object.key("audio").audioBlock(&block.Value)
	case *btypes.ContentBlockMemberToolUse:
		object.key("toolUse").toolUseBlock(&block.Value)
	case *btypes.ContentBlockMemberToolResult:
		object.key("toolResult").toolResultBlock(&block.Value)
	case *btypes.ContentBlockMemberGuardContent:
		object.key("guardContent").guardrailConverseContentBlock(block.Value)
	case *btypes.ContentBlockMemberCachePoint:
		object.key("cachePoint").cachePointBlock(&block.Value)
	case *btypes.ContentBlockMemberReasoningContent:
		object.key("reasoningContent").reasoningContentBlock(block.Value)
	case *btypes.ContentBlockMemberCitationsContent:
		object.key("citationsContent").citationsContentBlock(&block.Value)
	case *btypes.ContentBlockMemberSearchResult:
		object.key("searchResult").searchResultBlock(&block.Value)
	default:
		wire.unknown("ContentBlock", block)
		return
	}
	object.close()
}

func (wire *bedrockWire) systemContentBlock(block btypes.SystemContentBlock) {
	object := wire.object()
	switch block := block.(type) {
	case *btypes.SystemContentBlockMemberText:
		object.key("text").str(block.Value)
	case *btypes.SystemContentBlockMemberGuardContent:
		object.key("guardContent").guardrailConverseContentBlock(block.Value)
	case *btypes.SystemContentBlockMemberCachePoint:
		object.key("cachePoint").cachePointBlock(&block.Value)
	default:
		wire.unknown("SystemContentBlock", block)
		return
	}
	object.close()
}

func (wire *bedrockWire) imageBlock(block *btypes.ImageBlock) {
	object := wire.object()
	object.enum("format", string(block.Format))
	if block.Source != nil {
		source := object.key("source").object()
		switch value := block.Source.(type) {
		case *btypes.ImageSourceMemberBytes:
			source.blob("bytes", value.Value)
		case *btypes.ImageSourceMemberS3Location:
			source.key("s3Location").s3Location(&value.Value)
		default:
			wire.unknown("ImageSource", value)
			return
		}
		source.close()
	}
	if block.Error != nil {
		object.key("error").errorBlock(block.Error)
	}
	object.close()
}

func (wire *bedrockWire) s3Location(location *btypes.S3Location) {
	object := wire.object()
	object.str("uri", location.Uri)
	object.str("bucketOwner", location.BucketOwner)
	object.close()
}

func (wire *bedrockWire) errorBlock(block *btypes.ErrorBlock) {
	object := wire.object()
	object.str("message", block.Message)
	object.close()
}

func (wire *bedrockWire) citationsConfig(config *btypes.CitationsConfig) {
	object := wire.object()
	object.boolean("enabled", config.Enabled)
	object.close()
}

func (wire *bedrockWire) documentBlock(block *btypes.DocumentBlock) {
	object := wire.object()
	object.str("name", block.Name)
	if block.Source != nil {
		source := object.key("source").object()
		switch value := block.Source.(type) {
		case *btypes.DocumentSourceMemberBytes:
			source.blob("bytes", value.Value)
		case *btypes.DocumentSourceMemberS3Location:
			source.key("s3Location").s3Location(&value.Value)
		case *btypes.DocumentSourceMemberText:
			source.key("text").str(value.Value)
		case *btypes.DocumentSourceMemberContent:
			bedrockList(source.key("content"), value.Value, func(wire *bedrockWire, block btypes.DocumentContentBlock) {
				object := wire.object()
				text, ok := block.(*btypes.DocumentContentBlockMemberText)
				if !ok {
					wire.unknown("DocumentContentBlock", block)
					return
				}
				object.key("text").str(text.Value)
				object.close()
			})
		default:
			wire.unknown("DocumentSource", value)
			return
		}
		source.close()
	}
	object.enum("format", string(block.Format))
	object.str("context", block.Context)
	if block.Citations != nil {
		object.key("citations").citationsConfig(block.Citations)
	}
	object.close()
}

func (wire *bedrockWire) videoBlock(block *btypes.VideoBlock) {
	object := wire.object()
	object.enum("format", string(block.Format))
	if block.Source != nil {
		source := object.key("source").object()
		switch value := block.Source.(type) {
		case *btypes.VideoSourceMemberBytes:
			source.blob("bytes", value.Value)
		case *btypes.VideoSourceMemberS3Location:
			source.key("s3Location").s3Location(&value.Value)
		default:
			wire.unknown("VideoSource", value)
			return
		}
		source.close()
	}
	object.close()
}

func (wire *bedrockWire) audioBlock(block *btypes.AudioBlock) {
	object := wire.object()
	object.enum("format", string(block.Format))
	if block.Source != nil {
		source := object.key("source").object()
		switch value := block.Source.(type) {
		case *btypes.AudioSourceMemberBytes:
			source.blob("bytes", value.Value)
		case *btypes.AudioSourceMemberS3Location:
			source.key("s3Location").s3Location(&value.Value)
		default:
			wire.unknown("AudioSource", value)
			return
		}
		source.close()
	}
	if block.Error != nil {
		object.key("error").errorBlock(block.Error)
	}
	object.close()
}

func (wire *bedrockWire) toolUseBlock(block *btypes.ToolUseBlock) {
	object := wire.object()
	object.str("toolUseId", block.ToolUseId)
	object.str("name", block.Name)
	object.document("input", block.Input)
	object.enum("type", string(block.Type))
	object.close()
}

func (wire *bedrockWire) toolResultBlock(block *btypes.ToolResultBlock) {
	object := wire.object()
	object.str("toolUseId", block.ToolUseId)
	if block.Content != nil {
		bedrockList(object.key("content"), block.Content, (*bedrockWire).toolResultContentBlock)
	}
	object.enum("status", string(block.Status))
	object.str("type", block.Type)
	object.close()
}

func (wire *bedrockWire) toolResultContentBlock(block btypes.ToolResultContentBlock) {
	object := wire.object()
	switch block := block.(type) {
	case *btypes.ToolResultContentBlockMemberJson:
		object.document("json", block.Value)
	case *btypes.ToolResultContentBlockMemberText:
		object.key("text").str(block.Value)
	case *btypes.ToolResultContentBlockMemberImage:
		object.key("image").imageBlock(&block.Value)
	case *btypes.ToolResultContentBlockMemberDocument:
		object.key("document").documentBlock(&block.Value)
	case *btypes.ToolResultContentBlockMemberVideo:
		object.key("video").videoBlock(&block.Value)
	case *btypes.ToolResultContentBlockMemberSearchResult:
		object.key("searchResult").searchResultBlock(&block.Value)
	default:
		wire.unknown("ToolResultContentBlock", block)
		return
	}
	object.close()
}

func (wire *bedrockWire) searchResultBlock(block *btypes.SearchResultBlock) {
	object := wire.object()
	object.str("source", block.Source)
	object.str("title", block.Title)
	if block.Content != nil {
		bedrockList(object.key("content"), block.Content, func(wire *bedrockWire, content btypes.SearchResultContentBlock) {
			object := wire.object()
			object.str("text", content.Text)
			object.close()
		})
	}
	if block.Citations != nil {
		object.key("citations").citationsConfig(block.Citations)
	}
	object.close()
}

func (wire *bedrockWire) guardrailConverseContentBlock(block btypes.GuardrailConverseContentBlock) {
	object := wire.object()
	switch block := block.(type) {
	case *btypes.GuardrailConverseContentBlockMemberText:
		text := object.key("text").object()
		text.str("text", block.Value.Text)
		if block.Value.Qualifiers != nil {
			bedrockList(text.key("qualifiers"), block.Value.Qualifiers, func(wire *bedrockWire, qualifier btypes.GuardrailConverseContentQualifier) {
				wire.str(string(qualifier))
			})
		}
		text.close()
	case *btypes.GuardrailConverseContentBlockMemberImage:
		image := object.key("image").object()
		image.enum("format", string(block.Value.Format))
		if block.Value.Source != nil {
			source := image.key("source").object()
			bytes, ok := block.Value.Source.(*btypes.GuardrailConverseImageSourceMemberBytes)
			if !ok {
				wire.unknown("GuardrailConverseImageSource", block.Value.Source)
				return
			}
			source.blob("bytes", bytes.Value)
			source.close()
		}
		image.close()
	default:
		wire.unknown("GuardrailConverseContentBlock", block)
		return
	}
	object.close()
}

func (wire *bedrockWire) cachePointBlock(block *btypes.CachePointBlock) {
	object := wire.object()
	object.enum("type", string(block.Type))
	object.enum("ttl", string(block.Ttl))
	object.close()
}

func (wire *bedrockWire) reasoningContentBlock(block btypes.ReasoningContentBlock) {
	object := wire.object()
	switch block := block.(type) {
	case *btypes.ReasoningContentBlockMemberReasoningText:
		text := object.key("reasoningText").object()
		text.str("text", block.Value.Text)
		text.str("signature", block.Value.Signature)
		text.close()
	case *btypes.ReasoningContentBlockMemberRedactedContent:
		object.blob("redactedContent", block.Value)
	default:
		wire.unknown("ReasoningContentBlock", block)
		return
	}
	object.close()
}

func (wire *bedrockWire) citationsContentBlock(block *btypes.CitationsContentBlock) {
	object := wire.object()
	if block.Content != nil {
		bedrockList(object.key("content"), block.Content, func(wire *bedrockWire, content btypes.CitationGeneratedContent) {
			object := wire.object()
			text, ok := content.(*btypes.CitationGeneratedContentMemberText)
			if !ok {
				wire.unknown("CitationGeneratedContent", content)
				return
			}
			object.key("text").str(text.Value)
			object.close()
		})
	}
	if block.Citations != nil {
		bedrockList(object.key("citations"), block.Citations, (*bedrockWire).citation)
	}
	object.close()
}

func (wire *bedrockWire) citation(citation btypes.Citation) {
	object := wire.object()
	object.str("title", citation.Title)
	object.str("source", citation.Source)
	if citation.SourceContent != nil {
		bedrockList(object.key("sourceContent"), citation.SourceContent, func(wire *bedrockWire, content btypes.CitationSourceContent) {
			object := wire.object()
			text, ok := content.(*btypes.CitationSourceContentMemberText)
			if !ok {
				wire.unknown("CitationSourceContent", content)
				return
			}
			object.key("text").str(text.Value)
			object.close()
		})
	}
	if citation.Location != nil {
		object.key("location").citationLocation(citation.Location)
	}
	object.close()
}

func (wire *bedrockWire) citationLocation(location btypes.CitationLocation) {
	object := wire.object()
	span := func(name string, index *int32, indexName string, start, end *int32) {
		value := object.key(name).object()
		value.integer(indexName, index)
		value.integer("start", start)
		value.integer("end", end)
		value.close()
	}
	switch location := location.(type) {
	case *btypes.CitationLocationMemberWeb:
		web := object.key("web").object()
		web.str("url", location.Value.Url)
		web.str("domain", location.Value.Domain)
		web.close()
	case *btypes.CitationLocationMemberDocumentChar:
		span("documentChar", location.Value.DocumentIndex, "documentIndex", location.Value.Start, location.Value.End)
	case *btypes.CitationLocationMemberDocumentPage:
		span("documentPage", location.Value.DocumentIndex, "documentIndex", location.Value.Start, location.Value.End)
	case *btypes.CitationLocationMemberDocumentChunk:
		span("documentChunk", location.Value.DocumentIndex, "documentIndex", location.Value.Start, location.Value.End)
	case *btypes.CitationLocationMemberSearchResultLocation:
		span("searchResultLocation", location.Value.SearchResultIndex, "searchResultIndex", location.Value.Start, location.Value.End)
	default:
		wire.unknown("CitationLocation", location)
		return
	}
	object.close()
}

func (wire *bedrockWire) inferenceConfiguration(config *btypes.InferenceConfiguration) {
	object := wire.object()
	object.integer("maxTokens", config.MaxTokens)
	object.float("temperature", config.Temperature)
	object.float("topP", config.TopP)
	object.strings("stopSequences", config.StopSequences)
	object.close()
}

func (wire *bedrockWire) toolConfiguration(config *btypes.ToolConfiguration) {
	object := wire.object()
	if config.Tools != nil {
		bedrockList(object.key("tools"), config.Tools, (*bedrockWire).tool)
	}
	if config.ToolChoice != nil {
		choice := object.key("toolChoice").object()
		switch value := config.ToolChoice.(type) {
		case *btypes.ToolChoiceMemberAuto:
			empty := choice.key("auto").object()
			empty.close()
		case *btypes.ToolChoiceMemberAny:
			empty := choice.key("any").object()
			empty.close()
		case *btypes.ToolChoiceMemberTool:
			tool := choice.key("tool").object()
			tool.str("name", value.Value.Name)
			tool.close()
		default:
			wire.unknown("ToolChoice", value)
			return
		}
		choice.close()
	}
	object.close()
}

func (wire *bedrockWire) tool(tool btypes.Tool) {
	object := wire.object()
	switch tool := tool.(type) {
	case *btypes.ToolMemberToolSpec:
		spec := object.key("toolSpec").object()
		spec.str("name", tool.Value.Name)
		if tool.Value.InputSchema != nil {
			schema := spec.key("inputSchema").object()
			json, ok := tool.Value.InputSchema.(*btypes.ToolInputSchemaMemberJson)
			if !ok {
				wire.unknown("ToolInputSchema", tool.Value.InputSchema)
				return
			}
			schema.document("json", json.Value)
			schema.close()
		}
		spec.str("description", tool.Value.Description)
		spec.boolean("strict", tool.Value.Strict)
		spec.close()
	case *btypes.ToolMemberSystemTool:
		system := object.key("systemTool").object()
		system.str("name", tool.Value.Name)
		system.close()
	case *btypes.ToolMemberCachePoint:
		object.key("cachePoint").cachePointBlock(&tool.Value)
	default:
		wire.unknown("Tool", tool)
		return
	}
	object.close()
}

func (wire *bedrockWire) guardrailStreamConfiguration(config *btypes.GuardrailStreamConfiguration) {
	object := wire.object()
	object.str("guardrailIdentifier", config.GuardrailIdentifier)
	object.str("guardrailVersion", config.GuardrailVersion)
	object.enum("trace", string(config.Trace))
	object.enum("streamProcessingMode", string(config.StreamProcessingMode))
	object.close()
}

func (wire *bedrockWire) promptVariableValues(value btypes.PromptVariableValues) {
	object := wire.object()
	text, ok := value.(*btypes.PromptVariableValuesMemberText)
	if !ok {
		wire.unknown("PromptVariableValues", value)
		return
	}
	object.key("text").str(text.Value)
	object.close()
}

func (wire *bedrockWire) outputConfig(config *btypes.OutputConfig) {
	object := wire.object()
	if format := config.TextFormat; format != nil {
		text := object.key("textFormat").object()
		text.enum("type", string(format.Type))
		if format.Structure != nil {
			structure := text.key("structure").object()
			schema, ok := format.Structure.(*btypes.OutputFormatStructureMemberJsonSchema)
			if !ok {
				wire.unknown("OutputFormatStructure", format.Structure)
				return
			}
			definition := structure.key("jsonSchema").object()
			definition.str("schema", schema.Value.Schema)
			definition.str("name", schema.Value.Name)
			definition.str("description", schema.Value.Description)
			definition.close()
			structure.close()
		}
		text.close()
	}
	object.close()
}
