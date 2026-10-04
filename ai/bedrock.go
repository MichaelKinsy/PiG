package ai

// Ports packages/ai/src/api/bedrock-converse-stream.ts
// ConverseStream uses the AWS SDK transport with request-scoped proxy, region, and credential selection.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	smithybearer "github.com/aws/smithy-go/auth/bearer"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// BedrockProvider implements the Provider interface backed by AWS
// Bedrock's ConverseStream API.
type BedrockProvider struct {
	model          string
	modelName      string
	baseURL        string
	selectedModel  *Model
	converseStream func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// NewBedrockProvider constructs a Bedrock provider for the given model
// id. The model id is forwarded verbatim to the ConverseStream call,
// supporting both base model ids (e.g. "anthropic.claude-3-5-haiku-v1")
// and inference-profile ARNs.
func NewBedrockProvider(model, baseURL string) *BedrockProvider {
	return &BedrockProvider{model: model, baseURL: baseURL}
}

// NewBedrockProviderWithName constructs a Bedrock provider with optional
// display/model name metadata. The additional name is used only for parity
// with upstream Bedrock capability detection on application inference
// profiles whose ARN does not reveal the underlying Claude model family.
func NewBedrockProviderWithName(model, modelName, baseURL string) *BedrockProvider {
	return &BedrockProvider{model: model, modelName: modelName, baseURL: baseURL}
}

// NewBedrockProviderWithModel retains the selected model's limits and compatibility metadata for requests and callbacks.
// Ports packages/ai/src/api/bedrock-converse-stream.ts (stream receives the caller's model, including overrides).
func NewBedrockProviderWithModel(model Model) *BedrockProvider {
	return &BedrockProvider{model: model.ID, modelName: model.DisplayName, baseURL: model.ProviderMeta.BaseURL, selectedModel: &model}
}

// ID returns the selected model's provider identity, or the canonical Bedrock identity for constructors without model metadata.
func (p *BedrockProvider) ID() string {
	if p.selectedModel != nil {
		return modelProviderID(p.selectedModel)
	}
	return "amazon-bedrock"
}

// Close releases any persistent resources held by the provider. The
// bedrockruntime client maintains no goroutines or sockets that
// outlive a call, so this is a no-op.
func (p *BedrockProvider) Close() error { return nil }

// Stream runs ConverseStream and translates its events into one terminal assistant result. Request failures preserve available HTTP status, AWS error code, and request identity as diagnostics. An explicit CacheRetention overrides the legacy environment preference. Nil contexts are rejected before stream construction.
func (p *BedrockProvider) Stream(ctx context.Context, transcript TranscriptContext, opts StreamOptions) (*AssistantMessageEventStream, error) {
	requestErr := validateProviderRequest(ctx, transcript)
	if requestErr != nil && (ctx == nil || ctx.Err() == nil) {
		return nil, fmt.Errorf("amazon-bedrock: invalid transcript: %w", requestErr)
	}
	builder := newObservedProviderBuilder(ctx, APIBedrockConverseStream, p.ID(), p.model)
	builder.modelCost = opts.ModelCost
	builder.setProviderEventObserver(opts, providerEventModel(p.selectedModel, APIBedrockConverseStream, p.ID(), p.model))
	if requestErr != nil {
		failBedrockResponse(ctx, builder, requestErr, "", false)
		return builder.stream, nil
	}
	cfg, err := loadBedrockConfig(ctx, p.baseURL, p.model, opts)
	if err != nil {
		if ctx.Err() != nil {
			failBedrockResponse(ctx, builder, err, "", false)
			return builder.stream, nil
		}
		return nil, fmt.Errorf("amazon-bedrock: %w", err)
	}
	client := bedrockruntime.NewFromConfig(cfg, func(options *bedrockruntime.Options) {
		if cfg.BearerAuthTokenProvider != nil {
			options.BearerAuthSigner = bedrockBearerSigner{}
		}
	})

	modelMeta := &Model{ID: p.model, DisplayName: p.modelName}
	if p.selectedModel != nil {
		modelMeta = new(*p.selectedModel)
	} else if generated, ok := LookupModel(p.ID() + "/" + p.model); ok {
		modelMeta = generated.ToModel()
	}
	if modelMeta.DisplayName == "" {
		modelMeta.DisplayName = p.modelName
	}
	// Bedrock has no mid-conversation system messages. Later system messages always fold into the leading prompt, whatever the model compat claims.
	resolved := prepareProviderToolFlow(CollapseSystemMessages(transcript))
	messages := resolved.Messages()
	cacheRetention := resolveBedrockCacheRetention(opts.CacheRetention, opts.Env)
	system := buildBedrockSystemPrompt(p.model, p.modelName, GetCurrentSystemPrompt(messages[:min(1, len(messages))]), cacheRetention, opts.Env)
	conversation := WithoutInitialSystemMessage(messages)
	convertedMessages, err := convertBedrockMessages(conversation, p.model, p.modelName, cacheRetention, opts.Env)
	if err != nil {
		return nil, fmt.Errorf("amazon-bedrock: convert messages: %w", err)
	}
	supportsStrictTools := modelMeta.ProviderMeta.Compat != nil && modelMeta.ProviderMeta.Compat.SupportsStrictMode != nil && *modelMeta.ProviderMeta.Compat.SupportsStrictMode
	tools, err := convertBedrockTools(GetCurrentTools(messages), opts.ToolChoice, supportsStrictTools)
	if err != nil {
		return nil, fmt.Errorf("amazon-bedrock: convert tools: %w", err)
	}

	inf := &btypes.InferenceConfiguration{}
	maxTokens := opts.MaxTokens
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:inferenceMaxTokens
	if maxTokens == 0 && isAnthropicClaudeBedrockModel(modelMeta.ID, modelMeta.DisplayName) {
		maxTokens = modelMeta.Capabilities.MaxOutputTokens
	}
	if maxTokens > 0 {
		mt := int32(maxTokens)
		inf.MaxTokens = &mt
	}
	if opts.TemperatureSet || opts.Temperature != 0 {
		t := float32(opts.Temperature)
		inf.Temperature = &t
	}

	input := &bedrockruntime.ConverseStreamInput{
		ModelId:         aws.String(p.model),
		Messages:        convertedMessages,
		System:          system,
		InferenceConfig: inf,
		ToolConfig:      tools,
		RequestMetadata: opts.RequestMetadata,
	}
	if extra := buildBedrockAdditionalFields(modelMeta, p.modelName, opts); extra != nil {
		input.AdditionalModelRequestFields = bdoc.NewLazyDocument(extra)
	}
	if opts.OnPayload != nil {
		next, err := opts.OnPayload(input, modelMeta)
		if err != nil {
			return nil, fmt.Errorf("amazon-bedrock: onPayload: %w", err)
		}
		switch v := next.(type) {
		case nil:
		case *bedrockruntime.ConverseStreamInput:
			input = v
		case bedrockruntime.ConverseStreamInput:
			input = &v
		default:
			return nil, fmt.Errorf("amazon-bedrock: onPayload returned %T, want *bedrockruntime.ConverseStreamInput", next)
		}
	}

	converse := p.converseStream
	if converse == nil {
		converse = client.ConverseStream
	}
	// Pi returns the event stream before the request settles: the async body of `stream` starts at its first await (bedrock-converse-stream.ts:129-330).
	requestContext, abort := context.WithCancel(ctx)
	builder.abort = abort
	go builder.runResponse(func() error {
		p.streamResponse(ctx, requestContext, converse, input, opts, modelMeta, builder)
		return nil
	})
	return builder.stream, nil
}

// streamResponse sends the request and runs the stream loop. A body the transport can observe is read by the provider as an executor turn; any other body is read by the SDK's event-stream reader.
func (p *BedrockProvider) streamResponse(ctx, requestContext context.Context, converse func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error), input *bedrockruntime.ConverseStreamInput, opts StreamOptions, modelMeta *Model, builder *assistantStreamBuilder) {
	tap := &bedrockBodyTap{}
	defer tap.close()
	resp, err := converse(requestContext, input, tap.option, func(options *bedrockruntime.Options) {
		if opts.OnResponse != nil {
			options.APIOptions = append(options.APIOptions, func(stack *smithymiddleware.Stack) error {
				return stack.Deserialize.Add(smithymiddleware.DeserializeMiddlewareFunc("PiGProviderResponse", func(ctx context.Context, input smithymiddleware.DeserializeInput, next smithymiddleware.DeserializeHandler) (smithymiddleware.DeserializeOutput, smithymiddleware.Metadata, error) {
					output, metadata, err := next.HandleDeserialize(ctx, input)
					if err == nil {
						if response, ok := output.RawResponse.(*smithyhttp.Response); ok {
							err = observeProviderResponse(ctx, opts, response.Response, modelMeta)
							if err != nil {
								if result, ok := output.Result.(*bedrockruntime.ConverseStreamOutput); ok && result.GetStream() != nil {
									_ = result.GetStream().Close()
								} else {
									_ = response.Body.Close()
								}
							}
						}
					}
					return output, metadata, err
				}), smithymiddleware.Before)
			})
		}
		options.APIOptions = append(options.APIOptions, withBedrockHeaders(opts.Headers))
	})
	if err != nil {
		failBedrockResponse(ctx, builder, err, "", false)
		return
	}
	stream := resp.GetStream()
	if body := tap.body(); body != nil {
		if stream != nil {
			_ = stream.Close()
		}
		requestID, _ := awsmiddleware.GetRequestIDMetadata(resp.ResultMetadata)
		builder.managed = true
		_ = builder.responseTurn(func() error {
			p.runBedrockPipeline(ctx, builder.turn, builder, body, normalizeBedrockDiagnosticValue(requestID), opts.OnResponse != nil)
			return nil
		})
		return
	}
	p.parseBedrockStream(ctx, resp, builder)
}

func maxTokensPtr(v int) *int {
	if v <= 0 {
		return nil
	}
	return &v
}

// ─── Region / endpoint / auth resolution ─────────────────────────────────

// loadBedrockConfig preserves explicit/scoped profile ownership, ambient key precedence, and ARN region precedence. A configured region or ambient profile unpins standard AWS endpoints; caller-supplied custom endpoints remain fixed.
func loadBedrockConfig(ctx context.Context, baseURL, modelID string, options StreamOptions) (aws.Config, error) {
	env := options.Env
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(transport *http.Transport) {
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			return ResolveHTTPProxyURLForTarget(request.URL.String(), env)
		}
		if transport.DialContext != nil {
			transport.DialContext = bedrockObservedDial(transport.DialContext)
		}
	})
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithHTTPClient(httpClient)}

	configuredRegion := firstNonEmptyString(options.Region, getProviderEnvValue("AWS_REGION", env), getProviderEnvValue("AWS_DEFAULT_REGION", env))
	ambientProfile := getProviderEnvValue("AWS_PROFILE", nil)
	useEndpoint := bedrockEndpointRegion(baseURL) == "" || (configuredRegion == "" && ambientProfile == "")
	regionEnv := env
	if options.Region != "" {
		regionEnv = mergeProviderEnv(env, ProviderEnv{"AWS_REGION": options.Region})
	}
	region := resolveBedrockRegionWithEnv(baseURL, modelID, regionEnv)
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if trimmed := strings.TrimSpace(baseURL); useEndpoint && trimmed != "" {
		opts = append(opts, awsconfig.WithBaseEndpoint(strings.TrimRight(trimmed, "/")))
	}
	optionsProfile := firstNonEmptyString(options.Profile, env["AWS_PROFILE"])
	if profile := firstNonEmptyString(optionsProfile, ambientProfile); profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:stream
	// Explicit/scoped profiles own credential selection; ambient profiles do not displace env keys.
	skipAuth := getProviderEnvValue("AWS_BEDROCK_SKIP_AUTH", env) == "1"
	if skipAuth {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"dummy-access-key", "dummy-secret-key", "",
		)))
	} else if accessKey, secretKey := getProviderEnvValue("AWS_ACCESS_KEY_ID", env), getProviderEnvValue("AWS_SECRET_ACCESS_KEY", env); optionsProfile == "" && accessKey != "" && secretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, getProviderEnvValue("AWS_SESSION_TOKEN", env))))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS config: %w", err)
	}
	if token := firstNonEmptyString(options.APIKey, getProviderEnvValue("AWS_BEARER_TOKEN_BEDROCK", env)); token != "" && !skipAuth {
		cfg.BearerAuthTokenProvider = staticBearerTokenProvider(token)
		cfg.AuthSchemePreference = []string{"httpBearerAuth"}
	}
	return cfg, nil
}

// bedrockARNRegionRE extracts the region embedded in an inference-profile ARN
// (e.g. arn:aws:bedrock:us-west-2:...). Mirrors upstream amazon-bedrock.ts:146.
var bedrockARNRegionRE = lazyregexp.New(`^arn:aws(?:-[a-z0-9-]+)?:bedrock:([a-z0-9-]+):`)

func resolveBedrockRegion(baseURL, modelID string) string {
	return resolveBedrockRegionWithEnv(baseURL, modelID, nil)
}

func resolveBedrockRegionWithEnv(baseURL, modelID string, env ProviderEnv) string {
	// An inference-profile ARN's embedded region wins over AWS_REGION and the
	// rest of the chain (avoids conflicts with AWS_REGION set for other services).
	if m := bedrockARNRegionRE.FindStringSubmatch(modelID); m != nil {
		return m[1]
	}
	if r := getProviderEnvValue("AWS_REGION", env); r != "" {
		return r
	}
	if r := getProviderEnvValue("AWS_DEFAULT_REGION", env); r != "" {
		return r
	}
	if getProviderEnvValue("AWS_PROFILE", nil) != "" {
		return ""
	}
	if r := bedrockEndpointRegion(baseURL); r != "" {
		return r
	}
	return "us-east-1"
}

var bedrockEndpointHostRE = lazyregexp.New(`^bedrock-runtime(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)

func bedrockEndpointRegion(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	m := bedrockEndpointHostRE.FindStringSubmatch(strings.ToLower(u.Hostname()))
	if len(m) < 3 {
		return ""
	}
	return m[2]
}

// staticBearerTokenProvider implements aws.BearerTokenProvider with a
// fixed value.
type staticBearerTokenProvider string

func (s staticBearerTokenProvider) RetrieveBearerToken(_ context.Context) (smithybearer.Token, error) {
	return smithybearer.Token{Value: string(s)}, nil
}

// bedrockBearerSigner preserves bearer auth on caller-selected endpoints, including HTTP proxies, as the Node SDK does.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:stream
// Authentication never substitutes a different transport for the selected endpoint.
type bedrockBearerSigner struct{}

func (bedrockBearerSigner) SignWithBearerToken(_ context.Context, token smithybearer.Token, message smithybearer.Message) (smithybearer.Message, error) {
	request, ok := message.(*smithyhttp.Request)
	if !ok {
		return nil, fmt.Errorf("bedrock bearer auth: unexpected request type %T", message)
	}
	signed := request.Clone()
	signed.Header.Set("Authorization", "Bearer "+token.Value)
	return signed, nil
}

type bedrockHeadersMiddleware struct {
	headers ProviderHeaders
}

func (middleware bedrockHeadersMiddleware) ID() string { return "pi-ai-custom-headers" }

func isReservedBedrockHeader(name string) bool {
	name = strings.ToLower(name)
	return name == "authorization" || name == "host" || strings.HasPrefix(name, "x-amz-")
}

func (middleware bedrockHeadersMiddleware) HandleBuild(ctx context.Context, input smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
	request, ok := input.Request.(*smithyhttp.Request)
	if !ok || request == nil || request.Header == nil {
		return next.HandleBuild(ctx, input)
	}
	for name, value := range middleware.headers {
		if isReservedBedrockHeader(name) {
			continue
		}
		if value == nil {
			request.Header.Del(name)
			continue
		}
		request.Header.Set(name, *value)
	}
	return next.HandleBuild(ctx, input)
}

func withBedrockHeaders(headers ProviderHeaders) func(*smithymiddleware.Stack) error {
	return func(stack *smithymiddleware.Stack) error {
		if len(headers) == 0 {
			return nil
		}
		return stack.Build.Add(bedrockHeadersMiddleware{headers: headers}, smithymiddleware.After)
	}
}

// ─── Cache retention ─────────────────────────────────────────────────────

type bedrockCacheRetention string

const (
	bedrockCacheNone  bedrockCacheRetention = "none"
	bedrockCacheShort bedrockCacheRetention = "short"
	bedrockCacheLong  bedrockCacheRetention = "long"
)

// bedrockEmptyPlaceholder replaces user/tool-result content emptied by
// blank-text or surrogate filtering. Bedrock rejects empty content blocks,
// so the placeholder preserves the turn (and keeps a tool_use paired with
// its tool_result). Mirrors upstream bedrock-provider convertMessages.
const bedrockEmptyPlaceholder = "<empty>"

// resolveBedrockCacheRetention uses the explicit option first, then the legacy long-only environment preference, otherwise short.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:resolveCacheRetention
func resolveBedrockCacheRetention(cacheRetention CacheRetention, env ProviderEnv) bedrockCacheRetention {
	if cacheRetention != "" {
		return bedrockCacheRetention(cacheRetention)
	}
	if getProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return bedrockCacheLong
	}
	return bedrockCacheShort
}

func bedrockModelMatchCandidates(modelID, modelName string) []string {
	values := []string{modelID}
	if strings.TrimSpace(modelName) != "" {
		values = append(values, modelName)
	}
	out := make([]string, 0, len(values)*2)
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		if lower == "" {
			continue
		}
		out = append(out, lower)
		normalized := strings.NewReplacer(" ", "-", "_", "-", ".", "-", ":", "-").Replace(lower)
		if normalized != lower {
			out = append(out, normalized)
		}
	}
	return out
}

func isAnthropicClaudeBedrockModel(modelID, modelName string) bool {
	for _, candidate := range bedrockModelMatchCandidates(modelID, modelName) {
		if strings.Contains(candidate, "anthropic.claude") || strings.Contains(candidate, "anthropic/claude") || strings.Contains(candidate, "claude") {
			return true
		}
	}
	return false
}

// supportsBedrockPromptCaching checks the model and request-scoped force opt-in before falling back to the process environment.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:supportsPromptCaching
func supportsBedrockPromptCaching(modelID, modelName string, env ProviderEnv) bool {
	candidates := bedrockModelMatchCandidates(modelID, modelName)
	hasClaudeRef := false
	for _, candidate := range candidates {
		if strings.Contains(candidate, "claude") {
			hasClaudeRef = true
			break
		}
	}
	if !hasClaudeRef {
		// Allow forcing for application inference profiles whose ARNs
		// don't reveal the model name. Mirrors upstream.
		return getProviderEnvValue("AWS_BEDROCK_FORCE_CACHE", env) == "1"
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "fable-5") || strings.Contains(candidate, "opus-5") || strings.Contains(candidate, "sonnet-5") {
			return true
		}
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "-4-") || strings.Contains(candidate, "claude-3-7-sonnet") || strings.Contains(candidate, "claude-3-5-haiku") {
			return true
		}
	}
	return false
}

func supportsBedrockThinkingSignatureWithName(modelID, modelName string) bool {
	return isAnthropicClaudeBedrockModel(modelID, modelName)
}

func supportsBedrockAdaptiveThinkingWithName(modelID, modelName string) bool {
	for _, candidate := range bedrockModelMatchCandidates(modelID, modelName) {
		if strings.Contains(candidate, "opus-4-6") || strings.Contains(candidate, "opus-4-7") || strings.Contains(candidate, "opus-4-8") || strings.Contains(candidate, "opus-5") || strings.Contains(candidate, "sonnet-4-6") || strings.Contains(candidate, "sonnet-5") || strings.Contains(candidate, "fable-5") {
			return true
		}
	}
	return false
}

// ─── Message conversion ──────────────────────────────────────────────────

func buildBedrockSystemPrompt(modelID, modelName, systemPrompt string, cacheRetention bedrockCacheRetention, env ProviderEnv) []btypes.SystemContentBlock {
	if systemPrompt == "" {
		return nil
	}
	blocks := []btypes.SystemContentBlock{
		&btypes.SystemContentBlockMemberText{Value: sanitizeSurrogates(systemPrompt)},
	}
	if cacheRetention != bedrockCacheNone && supportsBedrockPromptCaching(modelID, modelName, env) {
		blocks = append(blocks, &btypes.SystemContentBlockMemberCachePoint{
			Value: btypes.CachePointBlock{
				Type: btypes.CachePointTypeDefault,
				Ttl:  bedrockCacheTTL(cacheRetention),
			},
		})
	}
	return blocks
}

func bedrockCacheTTL(retention bedrockCacheRetention) btypes.CacheTTL {
	if retention == bedrockCacheLong {
		return btypes.CacheTTLOneHour
	}
	return ""
}

// convertBedrockMessages mirrors upstream convertMessages + the
// transformMessages tool-id normalization + the consecutive-toolResult
// coalescing into a single user message that Bedrock requires. User, assistant and tool-result text uses ECMAScript emptiness checks without trimming nonblank payloads. System messages are skipped: the caller has already folded them into the leading prompt.
func convertBedrockMessages(msgs []Message, modelID, modelName string, cacheRetention bedrockCacheRetention, env ProviderEnv) ([]btypes.Message, error) {
	out := make([]btypes.Message, 0, len(msgs))

	for i := 0; i < len(msgs); i++ {
		switch message := msgs[i].(type) {
		case UserMessage:
			blocks, err := bedrockUserContent(message.Content)
			if err != nil {
				return nil, err
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, btypes.Message{Role: btypes.ConversationRoleUser, Content: blocks})

		case AssistantMessage:
			blocks, err := bedrockAssistantContent(message.Content, modelID, modelName)
			if err != nil {
				return nil, err
			}
			if len(blocks) == 0 {
				// Mirrors upstream: skip empty assistant messages.
				continue
			}
			out = append(out, btypes.Message{Role: btypes.ConversationRoleAssistant, Content: blocks})

		case ToolResultMessage:
			// Collect this tool-result + all immediately following
			// tool-result messages into a single user message, exactly
			// like upstream's toolResult coalescing.
			combined, consumed, err := bedrockToolResultRun(msgs, i)
			if err != nil {
				return nil, err
			}
			if len(combined) > 0 {
				out = append(out, btypes.Message{Role: btypes.ConversationRoleUser, Content: combined})
			}
			i += consumed - 1

		default:
			continue
		}
	}

	// Final cache point on the last user message (mirrors upstream).
	if cacheRetention != bedrockCacheNone && supportsBedrockPromptCaching(modelID, modelName, env) && len(out) > 0 {
		last := &out[len(out)-1]
		if last.Role == btypes.ConversationRoleUser {
			last.Content = append(last.Content, &btypes.ContentBlockMemberCachePoint{
				Value: btypes.CachePointBlock{Type: btypes.CachePointTypeDefault, Ttl: bedrockCacheTTL(cacheRetention)},
			})
		}
	}
	return out, nil
}

func bedrockUserContent(content UserContent) ([]btypes.ContentBlock, error) {
	switch content := content.(type) {
	case UserText:
		text := sanitizeSurrogates(string(content))
		if trimJSWhitespace(text) == "" {
			text = bedrockEmptyPlaceholder
		}
		return []btypes.ContentBlock{
			&btypes.ContentBlockMemberText{Value: text},
		}, nil
	case UserContentBlocks:
		out := make([]btypes.ContentBlock, 0, len(content))
		for _, block := range content {
			switch block := block.(type) {
			case TextContent:
				text := sanitizeSurrogates(block.Text)
				if trimJSWhitespace(text) == "" {
					continue
				}
				out = append(out, &btypes.ContentBlockMemberText{Value: text})
			case ImageContent:
				img, err := bedrockImageBlock(block.MimeType, block.Data)
				if err != nil {
					return nil, err
				}
				out = append(out, &btypes.ContentBlockMemberImage{Value: img})
			default:
				continue
			}
		}
		// Bedrock rejects empty content; replace a user message emptied by
		// filtering (blank/unknown blocks) with a placeholder rather than
		// dropping the turn. Mirrors upstream convertMessages.
		if len(out) == 0 {
			out = append(out, &btypes.ContentBlockMemberText{Value: bedrockEmptyPlaceholder})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported user content shape %T", content)
	}
}

func bedrockAssistantContent(blocks []AssistantContentBlock, modelID, modelName string) ([]btypes.ContentBlock, error) {
	out := make([]btypes.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block := block.(type) {
		case TextContent:
			text := sanitizeSurrogates(block.Text)
			if trimJSWhitespace(text) == "" {
				continue
			}
			out = append(out, &btypes.ContentBlockMemberText{Value: text})
		case ToolCall:
			arguments, err := sanitizeBedrockDocument(block.Arguments)
			if err != nil {
				return nil, err
			}
			if arguments == nil {
				arguments = map[string]any{}
			}
			out = append(out, &btypes.ContentBlockMemberToolUse{Value: btypes.ToolUseBlock{
				ToolUseId: aws.String(normalizeBedrockToolID(block.ID)),
				Name:      aws.String(block.Name),
				Input:     bdoc.NewLazyDocument(arguments),
			}})
		case ThinkingContent:
			// upstream: packages/ai/src/api/bedrock-converse-stream.ts:convertMessages
			if block.Redacted {
				if data := decodeBedrockRedactedContent(block.ThinkingSignature); len(data) > 0 {
					out = append(out, &btypes.ContentBlockMemberReasoningContent{
						Value: &btypes.ReasoningContentBlockMemberRedactedContent{Value: data},
					})
				}
				continue
			}
			thinking := sanitizeSurrogates(block.Thinking)
			if trimJSWhitespace(thinking) == "" {
				continue
			}
			if supportsBedrockThinkingSignatureWithName(modelID, modelName) {
				if trimJSWhitespace(block.ThinkingSignature) == "" {
					// Mirror upstream fallback: emit plain text when a
					// thinking block lacks its signature.
					out = append(out, &btypes.ContentBlockMemberText{Value: thinking})
				} else {
					out = append(out, &btypes.ContentBlockMemberReasoningContent{
						Value: &btypes.ReasoningContentBlockMemberReasoningText{
							Value: btypes.ReasoningTextBlock{
								Text:      aws.String(thinking),
								Signature: aws.String(block.ThinkingSignature),
							},
						},
					})
				}
			} else {
				out = append(out, &btypes.ContentBlockMemberReasoningContent{
					Value: &btypes.ReasoningContentBlockMemberReasoningText{
						Value: btypes.ReasoningTextBlock{Text: aws.String(thinking)},
					},
				})
			}
		default:
			continue
		}
	}
	return out, nil
}

// sanitizeBedrockDocument removes empty keys from a copied JSON tree, never from the retained tool call. The SDK rejects empty document map keys at serialization.
func sanitizeBedrockDocument(value any) (any, error) {
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return nil, err
	}
	return sanitizeBedrockDocumentValue(normalized)
}

func sanitizeBedrockDocumentValue(value any) (any, error) {
	switch value := value.(type) {
	case map[string]any:
		delete(value, "")
		for key, child := range value {
			converted, err := sanitizeBedrockDocumentValue(child)
			if err != nil {
				return nil, err
			}
			value[key] = converted
		}
		return value, nil
	case []any:
		for index, child := range value {
			converted, err := sanitizeBedrockDocumentValue(child)
			if err != nil {
				return nil, err
			}
			value[index] = converted
		}
		return value, nil
	case json.Number:
		return value.Float64()
	default:
		return value, nil
	}
}

// bedrockToolResultRun walks a span of consecutive tool-result messages
// starting at msgs[i] and produces a single content slice combining all
// of them, plus the number of messages consumed.
func bedrockToolResultRun(msgs []Message, i int) ([]btypes.ContentBlock, int, error) {
	out := []btypes.ContentBlock{}
	j := i
	for j < len(msgs) {
		result, ok := msgs[j].(ToolResultMessage)
		if !ok {
			break
		}
		block, err := bedrockToolResultBlock(result)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, block)
		j++
	}
	return out, j - i, nil
}

func bedrockToolResultBlock(message ToolResultMessage) (btypes.ContentBlock, error) {
	var parts []btypes.ToolResultContentBlock
	for _, block := range message.Content {
		switch block := block.(type) {
		case TextContent:
			if text := sanitizeSurrogates(block.Text); trimJSWhitespace(text) != "" {
				parts = append(parts, &btypes.ToolResultContentBlockMemberText{Value: text})
			}
		case ImageContent:
			image, err := bedrockImageBlock(block.MimeType, block.Data)
			if err != nil {
				return nil, err
			}
			parts = append(parts, &btypes.ToolResultContentBlockMemberImage{Value: image})
		}
	}
	if message.ToolCallID == "" {
		return nil, errors.New("amazon-bedrock: tool result missing toolCallId")
	}
	// Bedrock rejects empty tool-result content; a blank result would orphan
	// the preceding tool_use. Replace with a placeholder (upstream
	// convertMessages).
	if len(parts) == 0 {
		parts = append(parts, &btypes.ToolResultContentBlockMemberText{Value: bedrockEmptyPlaceholder})
	}
	status := btypes.ToolResultStatusSuccess
	if message.IsError {
		status = btypes.ToolResultStatusError
	}
	return &btypes.ContentBlockMemberToolResult{Value: btypes.ToolResultBlock{
		ToolUseId: aws.String(normalizeBedrockToolID(message.ToolCallID)),
		Content:   parts,
		Status:    status,
	}}, nil
}

var bedrockToolIDSanitizeRE = lazyregexp.New(`[^a-zA-Z0-9_-]`)

func normalizeBedrockToolID(id string) string {
	clean := bedrockToolIDSanitizeRE.ReplaceAllString(id, "_")
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}

func bedrockImageBlock(mime, dataB64 string) (btypes.ImageBlock, error) {
	var format btypes.ImageFormat
	switch mime {
	case "image/jpeg", "image/jpg":
		format = btypes.ImageFormatJpeg
	case "image/png":
		format = btypes.ImageFormatPng
	case "image/gif":
		format = btypes.ImageFormatGif
	case "image/webp":
		format = btypes.ImageFormatWebp
	default:
		return btypes.ImageBlock{}, fmt.Errorf("amazon-bedrock: unknown image type %q", mime)
	}
	bytes, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return btypes.ImageBlock{}, fmt.Errorf("amazon-bedrock: decode image: %w", err)
	}
	return btypes.ImageBlock{
		Format: format,
		Source: &btypes.ImageSourceMemberBytes{Value: bytes},
	}, nil
}

// ─── Tool config ─────────────────────────────────────────────────────────

func convertBedrockTools(tools []ToolSchema, toolChoice any, supportsStrictMode bool) (*btypes.ToolConfiguration, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	switch value := toolChoice.(type) {
	case nil, string, map[string]any:
	case JsonObject:
		toolChoice = map[string]any(value)
	case map[string]string:
		object := make(map[string]any, 2)
		for _, key := range []string{"type", "name"} {
			if field, present := value[key]; present {
				object[key] = field
			}
		}
		toolChoice = object
	default:
		value, err := normalizeJSONValue(toolChoice)
		if err != nil {
			return nil, err
		}
		toolChoice = value
	}
	choice, _ := toolChoice.(string)
	if choice == "none" {
		return nil, nil
	}
	out := make([]btypes.Tool, 0, len(tools))
	for _, tool := range tools {
		strict, err := resolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil)
		if err != nil {
			return nil, err
		}
		parameters, err := getJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, err
		}
		if parameters == nil {
			parameters = map[string]any{}
		}
		toolSpec := btypes.ToolSpecification{
			Name:        aws.String(tool.Name),
			Description: aws.String(tool.Description),
			InputSchema: &btypes.ToolInputSchemaMemberJson{Value: bdoc.NewLazyDocument(parameters)},
		}
		if strict != nil && *strict {
			toolSpec.Strict = new(true)
		}
		out = append(out, &btypes.ToolMemberToolSpec{Value: toolSpec})
	}
	config := &btypes.ToolConfiguration{Tools: out}
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:convertToolConfig
	switch choice {
	case "auto":
		config.ToolChoice = &btypes.ToolChoiceMemberAuto{Value: btypes.AutoToolChoice{}}
	case "any":
		config.ToolChoice = &btypes.ToolChoiceMemberAny{Value: btypes.AnyToolChoice{}}
	default:
		if object, ok := toolChoice.(map[string]any); ok && object["type"] == "tool" {
			name, ok := object["name"].(string)
			if !ok {
				return nil, fmt.Errorf("toolChoice tool name must be a string")
			}
			config.ToolChoice = &btypes.ToolChoiceMemberTool{Value: btypes.SpecificToolChoice{Name: aws.String(name)}}
		}
	}
	return config, nil
}

// ─── Additional model request fields (thinking) ──────────────────────────

// isGovCloudBedrockTarget follows configured region precedence independently of endpoint and ARN region resolution.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:isGovCloudBedrockTarget
func isGovCloudBedrockTarget(model *Model, opts StreamOptions) bool {
	region := opts.Region
	if region == "" {
		region = getProviderEnvValue("AWS_REGION", opts.Env)
	}
	if region == "" {
		region = getProviderEnvValue("AWS_DEFAULT_REGION", opts.Env)
	}
	id := strings.ToLower(model.ID)
	return strings.HasPrefix(strings.ToLower(region), "us-gov-") || strings.HasPrefix(id, "us-gov.") || strings.HasPrefix(id, "arn:aws-us-gov:")
}

// buildBedrockAdditionalFields omits thinking.display for GovCloud targets in both adaptive and budget-based requests.
func buildBedrockAdditionalFields(model *Model, modelName string, opts StreamOptions) map[string]any {
	if opts.Thinking == "" || opts.Thinking == ThinkingOff || !opts.IsReasoning {
		return nil
	}
	if model == nil || !isAnthropicClaudeBedrockModel(model.ID, modelName) {
		return nil
	}

	thinking := map[string]any{}
	isGovCloud := isGovCloudBedrockTarget(model, opts)
	if !isGovCloud {
		thinking["display"] = "summarized"
	}

	if supportsBedrockAdaptiveThinkingWithName(model.ID, modelName) {
		thinking["type"] = "adaptive"
		result := map[string]any{
			"thinking":      thinking,
			"output_config": map[string]any{"effort": mapBedrockThinkingEffort(model, opts.Thinking)},
		}
		// Replayed signed thinking blocks are bound to the system prompt and tools they were
		// created with. Bedrock 400s on replay after either changes unless stale blocks are
		// dropped, matching the Anthropic provider. Skipped on GovCloud like display.
		if !isGovCloud && supportsBedrockThinkingBlockBinding(model.ID, modelName) {
			thinking["block_binding"] = map[string]any{"prefix_mismatch_behavior": "drop_block"}
			result["anthropic_beta"] = []string{thinkingBindingControlsBeta}
		}
		return result
	}

	budget := 0
	if model.Capabilities.MaxOutputTokens > 0 {
		adjustedMax, adjustedBudget := AdjustMaxTokensForThinking(maxTokensPtr(opts.MaxTokens), model.Capabilities.MaxOutputTokens, string(opts.Thinking), nil)
		_ = adjustedMax // Mirrors upstream: the helper owns the budget calculation; Bedrock sends only the thinking budget here.
		budget = adjustedBudget
	} else {
		defaults := map[ThinkingLevel]int{
			ThinkingMinimal: 1024,
			ThinkingLow:     2048,
			ThinkingMedium:  8192,
			ThinkingHigh:    16384,
			ThinkingXHigh:   16384,
			ThinkingMax:     16384,
		}
		budget = defaults[opts.Thinking]
		if budget <= 0 {
			budget = defaults[ThinkingHigh]
		}
	}
	thinking["type"], thinking["budget_tokens"] = "enabled", budget
	result := map[string]any{"thinking": thinking}
	// Upstream defaults interleaved_thinking on for non-adaptive Claude.
	if opts.InterleavedThinking == nil || *opts.InterleavedThinking {
		result["anthropic_beta"] = []string{"interleaved-thinking-2025-05-14"}
	}
	return result
}

// supportsBedrockThinkingBlockBinding reports whether the model accepts
// thinking.block_binding. Opus 4.6 and Sonnet 4.6 reject it with
// "thinking.adaptive.block_binding: Extra inputs are not permitted".
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:supportsThinkingBlockBinding
func supportsBedrockThinkingBlockBinding(modelID, modelName string) bool {
	for _, s := range bedrockModelMatchCandidates(modelID, modelName) {
		if strings.Contains(s, "opus-4-7") || strings.Contains(s, "opus-4-8") || strings.Contains(s, "opus-5") || strings.Contains(s, "sonnet-5") || strings.Contains(s, "fable-5") {
			return true
		}
	}
	return false
}

// supportsNativeXhighEffort recognizes the Bedrock model families with native xhigh effort in both model IDs and display names.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:supportsNativeXhighEffort
func supportsNativeXhighEffort(model *Model) bool {
	if model == nil {
		return false
	}
	for _, s := range bedrockModelMatchCandidates(model.ID, model.DisplayName) {
		if strings.Contains(s, "opus-4-7") || strings.Contains(s, "opus-4-8") || strings.Contains(s, "opus-5") || strings.Contains(s, "sonnet-5") || strings.Contains(s, "fable-5") {
			return true
		}
	}
	return false
}

func mapBedrockThinkingEffort(model *Model, level ThinkingLevel) string {
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:mapThinkingLevelToEffort
	if level == ThinkingXHigh && supportsNativeXhighEffort(model) {
		return "xhigh"
	}
	if model != nil {
		if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
			return *mapped
		}
	}
	switch level {
	case ThinkingMinimal, ThinkingLow:
		return "low"
	case ThinkingMedium:
		return "medium"
	case ThinkingHigh:
		return "high"
	default:
		return "high"
	}
}

// ─── Stream parsing ──────────────────────────────────────────────────────

// activeBlock tracks the streaming state for a single content block
// indexed by Bedrock's contentBlockIndex.
type activeBlock struct {
	kind            string // "text" | "thinking" | "toolUse"
	toolID          string
	toolName        string
	partialJSON     strings.Builder
	contentIndex    int
	redactedContent []byte
}

// decodeBedrockRedactedContent accepts the base64 alphabet and ASCII whitespace
// accepted by atob. Invalid persisted signatures drop only the redacted block.
func decodeBedrockRedactedContent(signature string) []byte {
	signature = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\f':
			return -1
		default:
			return r
		}
	}, signature)
	var data []byte
	var err error
	if strings.Contains(signature, "=") {
		data, err = base64.StdEncoding.DecodeString(signature)
	} else {
		data, err = base64.RawStdEncoding.DecodeString(signature)
	}
	if err != nil {
		return nil
	}
	return data
}

func flushBedrockRedactedContent(block *activeBlock, builder *assistantStreamBuilder) {
	if len(block.redactedContent) == 0 {
		return
	}
	thinking := builder.partial.Content[block.contentIndex].(ThinkingContent)
	thinking.ThinkingSignature = base64.StdEncoding.EncodeToString(block.redactedContent)
	thinking.thinkingSignatureEmpty = false
	builder.partial.Content[block.contentIndex] = thinking
	block.redactedContent = nil
}

type bedrockEventStream interface {
	Events() <-chan btypes.ConverseStreamOutput
	Close() error
	Err() error
}

func (p *BedrockProvider) parseBedrockStream(ctx context.Context, resp *bedrockruntime.ConverseStreamOutput, builder *assistantStreamBuilder) {
	stream := resp.GetStream()
	if stream == nil {
		builder.fail(StopReasonError, errors.New("amazon-bedrock: nil stream"))
		return
	}
	requestID, _ := awsmiddleware.GetRequestIDMetadata(resp.ResultMetadata)
	p.parseBedrockEvents(ctx, stream, builder, normalizeBedrockDiagnosticValue(requestID))
}

func (p *BedrockProvider) parseBedrockEvents(ctx context.Context, stream bedrockEventStream, builder *assistantStreamBuilder, requestID string) {
	defer func() { _ = stream.Close() }()

	state := newBedrockStreamState(p, builder)
	events := stream.Events()
	for {
		select {
		case <-ctx.Done():
			state.finalizeBlocks()
			failBedrockResponse(ctx, builder, ctx.Err(), requestID, true)
			return
		case ev, ok := <-events:
			if !ok {
				state.finalizeBlocks()
				var streamErr error
				if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
					streamErr = err
				}
				state.finish(ctx, streamErr, requestID)
				return
			}
			if !bedrockStreamItemModeled(ev) {
				continue
			}
			err := builder.observeProviderEventData(ev) // bedrock-converse-stream.ts:297
			if err == nil {
				err = state.handle(ev)
			}
			if err != nil {
				state.finalizeBlocks()
				failBedrockResponse(ctx, builder, err, requestID, true)
				return
			}
		}
	}
}

// handleBedrockUsage mirrors the usage half of upstream handleMetadata: 1h
// cache writes are summed from cacheDetails and the usage is priced.
func handleBedrockUsage(u *btypes.TokenUsage, builder *assistantStreamBuilder, usage *Usage) {
	usage.Input = int(aws.ToInt32(u.InputTokens))
	usage.Output = int(aws.ToInt32(u.OutputTokens))
	usage.CacheRead = int(aws.ToInt32(u.CacheReadInputTokens))
	usage.CacheWrite = int(aws.ToInt32(u.CacheWriteInputTokens))
	usage.CacheWrite1h = nil
	if u.CacheDetails != nil {
		longWrite := 0
		for _, detail := range u.CacheDetails {
			if detail.Ttl == btypes.CacheTTLOneHour {
				longWrite += int(aws.ToInt32(detail.InputTokens))
			}
		}
		usage.CacheWrite1h = &longWrite
	}
	usage.TotalTokens = int(aws.ToInt32(u.TotalTokens))
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.Input + usage.Output
	}
	builder.calculateCost(usage)
	// output.usage is mutated in place (bedrock-converse-stream.ts:707-718), not replaced: earlier shallow copies observe the update.
	builder.partial.Usage = *usage
}

func (p *BedrockProvider) handleBedrockDelta(ev btypes.ContentBlockDeltaEvent, blocks map[int32]*activeBlock, builder *assistantStreamBuilder) {
	p.handleBedrockDeltaFields(ev, nil, blocks, builder)
}

// handleBedrockDeltaFields is handleContentBlockDelta. reasoning holds every reasoningContent member of the payload when the caller decoded the JSON itself; nil reads the single member of the SDK's union value.
func (p *BedrockProvider) handleBedrockDeltaFields(ev btypes.ContentBlockDeltaEvent, reasoning *bedrockReasoningFields, blocks map[int32]*activeBlock, builder *assistantStreamBuilder) {
	idx := aws.ToInt32(ev.ContentBlockIndex)
	switch d := ev.Delta.(type) {
	case *btypes.ContentBlockDeltaMemberText:
		// Lazily create a text block if needed (Bedrock does not emit
		// ContentBlockStart for text blocks).
		block, ok := blocks[idx]
		if !ok {
			block = &activeBlock{kind: "text", contentIndex: builder.textBlockStartWithBlock(TextContent{scratch: bedrockBlockScratch(builder, idx)})}
			blocks[idx] = block
		}
		if block.kind == "text" {
			builder.textBlockDelta(block.contentIndex, d.Value)
		}

	case *btypes.ContentBlockDeltaMemberToolUse:
		b, ok := blocks[idx]
		if !ok || b.kind != "toolUse" {
			return
		}
		b.partialJSON.WriteString(aws.ToString(d.Value.Input))
		builder.toolCallDelta(streamToolCallDelta{
			index: int(idx), id: b.toolID, name: b.toolName, argumentsDelta: aws.ToString(d.Value.Input),
		})

	case *btypes.ContentBlockDeltaMemberReasoningContent:
		block, ok := blocks[idx]
		if !ok {
			// upstream: packages/ai/src/api/bedrock-converse-stream.ts:handleContentBlockDelta
			block = &activeBlock{kind: "thinking", contentIndex: builder.thinkingBlockStartWithContent(ThinkingContent{thinkingSignatureEmpty: true, scratch: bedrockBlockScratch(builder, idx)})}
			blocks[idx] = block
		}
		if block.kind != "thinking" {
			return
		}
		if reasoning == nil {
			reasoning = bedrockReasoningFromUnion(d.Value)
		}
		// upstream: packages/ai/src/api/bedrock-converse-stream.ts:handleContentBlockDelta reads text, signature and redactedContent in this order, each when present.
		if reasoning.text != nil && *reasoning.text != "" {
			builder.thinkingBlockDelta(block.contentIndex, *reasoning.text)
		}
		if reasoning.signature != nil && *reasoning.signature != "" {
			thinking := builder.partial.Content[block.contentIndex].(ThinkingContent)
			if !thinking.Redacted {
				thinking.ThinkingSignature += *reasoning.signature
				thinking.thinkingSignatureEmpty = thinking.ThinkingSignature == ""
				builder.partial.Content[block.contentIndex] = thinking
			}
		}
		if len(reasoning.redacted) > 0 {
			thinking := builder.partial.Content[block.contentIndex].(ThinkingContent)
			if !thinking.Redacted {
				thinking.Redacted = true
				thinking.ThinkingSignature = ""
				thinking.thinkingSignatureEmpty = true
				builder.partial.Content[block.contentIndex] = thinking
				builder.thinkingBlockDelta(block.contentIndex, "[Reasoning redacted]")
			}
			block.redactedContent = append(block.redactedContent, reasoning.redacted...)
		}
	}
}

// bedrockReasoningFromUnion reads the members of an SDK ReasoningContentBlockDelta union value.
func bedrockReasoningFromUnion(value btypes.ReasoningContentBlockDelta) *bedrockReasoningFields {
	fields := &bedrockReasoningFields{}
	switch rc := value.(type) {
	case *btypes.ReasoningContentBlockDeltaMemberText:
		fields.text = &rc.Value
	case *btypes.ReasoningContentBlockDeltaMemberSignature:
		fields.signature = &rc.Value
	case *btypes.ReasoningContentBlockDeltaMemberRedactedContent:
		fields.redacted = rc.Value
	}
	return fields
}

func mapBedrockStopReason(reason string) (StopReason, string) {
	switch reason {
	case string(btypes.StopReasonEndTurn), string(btypes.StopReasonStopSequence):
		return StopReasonStop, ""
	case string(btypes.StopReasonMaxTokens), string(btypes.StopReasonModelContextWindowExceeded):
		return StopReasonLength, ""
	case string(btypes.StopReasonToolUse):
		return StopReasonToolUse, ""
	default:
		if reason != "" {
			return StopReasonError, "Provider stopped with: " + reason
		}
		return StopReasonError, ""
	}
}

// ─── Error formatting ────────────────────────────────────────────────────

// upstream: packages/ai/src/api/bedrock-converse-stream.ts:MAX_BEDROCK_DIAGNOSTIC_VALUE_CHARS
const maxBedrockDiagnosticValueChars = 200

func normalizeBedrockDiagnosticValue(value string) string {
	value = strings.TrimFunc(value, func(r rune) bool { return r == '\ufeff' || (r != '\u0085' && unicode.IsSpace(r)) })
	if value == "" || utf16Length(value) > maxBedrockDiagnosticValueChars {
		return ""
	}
	return value
}

func appendBedrockFailureDiagnostic(message *AssistantMessage, err error, fallbackRequestID string) {
	details := map[string]any{}
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && response.Response != nil && response.Response.Response != nil {
		details["status"] = response.HTTPStatusCode()
	}
	code := ""
	if apiError, ok := errors.AsType[smithy.APIError](err); ok {
		code = apiError.ErrorCode()
	}
	if strings.HasSuffix(code, "Exception") {
		if code = normalizeBedrockDiagnosticValue(code); code != "" {
			details["errorCode"] = code
		}
	}
	requestID := ""
	if response, ok := errors.AsType[*awshttp.ResponseError](err); ok {
		requestID = normalizeBedrockDiagnosticValue(response.RequestID)
	}
	if requestID == "" {
		requestID = normalizeBedrockDiagnosticValue(fallbackRequestID)
	}
	if requestID != "" {
		details["requestId"] = requestID
	}
	if len(details) == 0 {
		return
	}
	message.Diagnostics = append(message.Diagnostics, AssistantMessageDiagnostic{Type: "bedrock_response_failure", Timestamp: time.Now().UnixMilli(), Details: details})
}

func failBedrockResponse(ctx context.Context, builder *assistantStreamBuilder, err error, requestID string, streaming bool) {
	if ctx.Err() != nil {
		builder.fail(StopReasonAborted, errors.New("Request aborted"))
		return
	}
	appendBedrockFailureDiagnostic(builder.partial, err, requestID)
	transportMessage := "fetch failed"
	if streaming {
		transportMessage = "terminated"
	}
	mapped := mapBedrockTransportError(ctx, err, transportMessage)
	message := formatBedrockError(mapped)
	if streaming {
		if apiError, ok := errors.AsType[*smithy.GenericAPIError](mapped); ok {
			message = apiError.ErrorMessage() + bedrockDataRetentionHint(apiError.ErrorMessage())
		}
	}
	builder.fail(StopReasonError, errors.New(message))
}

// bedrockErrorPrefixes mirrors upstream's BEDROCK_ERROR_PREFIXES table
// verbatim. Retry classifiers downstream match patterns like
// "service.?unavailable" against these prefixes.
var bedrockErrorPrefixes = map[string]string{
	"InternalServerException":     "Internal server error",
	"ModelStreamErrorException":   "Model stream error",
	"ValidationException":         "Validation error",
	"ThrottlingException":         "Throttling error",
	"ServiceUnavailableException": "Service unavailable",
}

func formatBedrockError(err error) string {
	if err == nil {
		return ""
	}
	norm := NormalizeProviderError(err)
	core := FormatProviderError(norm)
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		prefix, ok := bedrockErrorPrefixes[apiErr.ErrorCode()]
		if !ok {
			prefix = apiErr.ErrorCode()
		}
		if norm.MessageCarriesBody {
			core = apiErr.ErrorMessage()
		}
		return fmt.Sprintf("%s: %s%s", prefix, core, bedrockDataRetentionHint(core))
	}
	return core + bedrockDataRetentionHint(core)
}

// bedrockDataRetentionHint appends a link to the AWS data-retention docs when a
// Bedrock error indicates the model rejected the configured data retention
// mode. Mirrors upstream formatBedrockError's dataRetentionHint (#5561).
func bedrockDataRetentionHint(message string) string {
	if strings.Contains(strings.ToLower(message), "data retention mode") {
		return " See https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html for supported data retention modes."
	}
	return ""
}

func init() {
	builtInProviders[APIBedrockConverseStream] = func(_, model, baseURL string) Provider {
		return NewBedrockProvider(model, baseURL)
	}
}
