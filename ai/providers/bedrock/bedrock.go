//go:build !pig_strip_bedrock_converse_stream

package bedrock

// Ports packages/ai/src/api/bedrock-converse-stream.ts
// ConverseStream uses the AWS SDK transport with request-scoped proxy, region, and credential selection.

import (
	"github.com/MichaelKinsy/PiG/ai"

	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/tlsdistrust"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
	smithybearer "github.com/aws/smithy-go/auth/bearer"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	awsconfig "github.com/MichaelKinsy/PiG/third_party/awssdk/config"
	"github.com/MichaelKinsy/PiG/third_party/awssdk/credentials"
	"github.com/MichaelKinsy/PiG/third_party/awssdk/credentials/stscreds"
	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/sts"
)

// BedrockProvider implements the Provider interface backed by AWS
// Bedrock's ConverseStream API.
type BedrockProvider struct {
	model          string
	modelName      string
	baseURL        string
	selectedModel  *ai.Model
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
func NewBedrockProviderWithModel(model ai.Model) *BedrockProvider {
	return &BedrockProvider{model: model.ID, modelName: model.DisplayName, baseURL: model.ProviderMeta.BaseURL, selectedModel: &model}
}

// ID returns the selected model's provider identity, or the canonical Bedrock identity for constructors without model metadata.
func (p *BedrockProvider) ID() string {
	if p.selectedModel != nil {
		return ai.ModelProviderID(p.selectedModel)
	}
	return "amazon-bedrock"
}

// Close releases any persistent resources held by the provider. The
// bedrockruntime client maintains no goroutines or sockets that
// outlive a call, so this is a no-op.
func (p *BedrockProvider) Close() error { return nil }

// Stream runs ConverseStream and translates its events into one terminal assistant result. Request failures preserve available HTTP status, AWS error code, and request identity as diagnostics. An explicit CacheRetention overrides the legacy environment preference. Nil contexts are rejected before stream construction.
func (p *BedrockProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	// pig divergence (D48): the agent loop asks for the tool-flow repair.
	if opts.RepairToolFlow {
		transcript = transcript.WithRepairedToolFlow()
	}
	requestErr := ai.ValidateProviderRequest(ctx, transcript)
	if requestErr != nil && (ctx == nil || ctx.Err() == nil) {
		return nil, fmt.Errorf("amazon-bedrock: invalid transcript: %w", requestErr)
	}
	builder := ai.NewObservedProviderBuilder(ctx, ai.APIBedrockConverseStream, p.ID(), p.model)
	builder.SetModelCost(opts.ModelCost)
	builder.SetProviderEventObserver(opts, ai.ProviderEventModel(p.selectedModel, ai.APIBedrockConverseStream, p.ID(), p.model))
	if requestErr != nil {
		failBedrockResponse(ctx, builder, requestErr, "", false)
		return builder.Stream(), nil
	}
	cfg, runtimeHTTPClient, err := loadBedrockConfig(ctx, p.baseURL, p.model, opts)
	if err != nil {
		if ctx.Err() != nil {
			failBedrockResponse(ctx, builder, err, "", false)
			return builder.Stream(), nil
		}
		return nil, fmt.Errorf("amazon-bedrock: %w", err)
	}
	client := bedrockruntime.NewFromConfig(cfg, bedrockClientOptions(cfg, runtimeHTTPClient))

	// A model outside the catalog is still a bedrock-converse-stream model of this provider, as every Pi Model is: transformMessages' isSameModel compares provider, api and id.
	modelMeta := &ai.Model{ID: p.model, DisplayName: p.modelName, ProviderMeta: ai.ProviderMetadata{ProviderID: p.ID(), API: ai.APIBedrockConverseStream}}
	if p.selectedModel != nil {
		modelMeta = new(*p.selectedModel)
	} else if generated, ok := ai.LookupModel(p.ID() + "/" + p.model); ok {
		modelMeta = generated.ToModel()
	}
	if modelMeta.DisplayName == "" {
		modelMeta.DisplayName = p.modelName
	}
	// Bedrock has no mid-conversation system messages. Later system messages always fold into the leading prompt, whatever the model compat claims.
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:convertMessages runs transformMessages with normalizeToolCallId: images the model cannot take, foreign thinking and foreign tool-call ids are rewritten there.
	messages := ai.TransformMessages(ai.CollapsedMessages(transcript), modelMeta, func(id string, _ *ai.Model, _ ai.AssistantMessage) string {
		return normalizeBedrockToolID(id)
	})
	cacheRetention := resolveBedrockCacheRetention(opts.CacheRetention, opts.Env)
	conversation := ai.BorrowedWithoutInitialSystemMessage(messages)
	supportsStrictTools := modelMeta.ProviderMeta.Compat != nil && modelMeta.ProviderMeta.Compat.SupportsStrictMode != nil && *modelMeta.ProviderMeta.Compat.SupportsStrictMode

	maxTokens := opts.MaxTokens
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:streamSimple sends adjustMaxTokensForThinking's maxTokens, clamped to the context, with the matching thinking budget.
	var adjusted int
	var thinkingBudget *int
	if !opts.RawThinking() {
		adjusted, thinkingBudget = adjustBedrockBudgetThinking(modelMeta, p.modelName, transcript, opts)
	}
	if adjusted > 0 {
		maxTokens = adjusted
	}
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:inferenceMaxTokens
	if maxTokens == 0 && isAnthropicClaudeBedrockModel(modelMeta.ID, modelMeta.DisplayName) {
		maxTokens = modelMeta.Capabilities.MaxOutputTokens
	}

	// buildInput is one pass of upstream's request loop up to the send: the converted request, passed through onPayload. It is built again, from
	// the transcript, for the strict tool fallback, because the hook may have changed the first request. hasPreferredStrictTools is true when the
	// request sends a strict: "prefer" tool strict.
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:stream (the request loop)
	buildInput := func(disablePreferredStrictTools bool) (input *bedrockruntime.ConverseStreamInput, hasPreferredStrictTools bool, err error) {
		tools, hasPreferredStrictTools, err := convertBedrockTools(ai.BorrowedCurrentTools(messages), opts.ToolChoice, supportsStrictTools, disablePreferredStrictTools)
		if err != nil {
			return nil, false, fmt.Errorf("amazon-bedrock: convert tools: %w", err)
		}
		convertedMessages, err := convertBedrockMessages(conversation, p.model, p.modelName, cacheRetention, opts.Env)
		if err != nil {
			return nil, false, fmt.Errorf("amazon-bedrock: convert messages: %w", err)
		}
		inf := &btypes.InferenceConfiguration{}
		if maxTokens > 0 {
			mt := int32(maxTokens)
			inf.MaxTokens = &mt
		}
		if opts.TemperatureSet || opts.Temperature != 0 {
			t := float32(opts.Temperature)
			inf.Temperature = &t
		}
		input = &bedrockruntime.ConverseStreamInput{
			ModelId:         aws.String(p.model),
			Messages:        convertedMessages,
			System:          buildBedrockSystemPrompt(p.model, p.modelName, ai.GetCurrentSystemPrompt(messages[:min(1, len(messages))]), cacheRetention, opts.Env),
			InferenceConfig: inf,
			ToolConfig:      tools,
			RequestMetadata: opts.RequestMetadata,
		}
		if extra := buildBedrockAdditionalFieldsWithBudget(modelMeta, p.modelName, opts, thinkingBudget); extra != nil {
			encoded, err := ai.MarshalSchemaWithOrder(extra, bedrockAdditionalFieldsOrder, "")
			if err != nil {
				return nil, false, fmt.Errorf("amazon-bedrock: additional model request fields: %w", err)
			}
			input.AdditionalModelRequestFields = newBedrockDocument(extra, encoded)
		}
		if opts.OnPayload != nil {
			next, err := opts.OnPayload(input, modelMeta)
			if err != nil {
				return nil, false, fmt.Errorf("amazon-bedrock: onPayload: %w", err)
			}
			switch v := next.(type) {
			case nil:
			case *bedrockruntime.ConverseStreamInput:
				input = v
			case bedrockruntime.ConverseStreamInput:
				input = &v
			default:
				return nil, false, fmt.Errorf("amazon-bedrock: onPayload returned %T, want *bedrockruntime.ConverseStreamInput", next)
			}
		}
		checkBedrockRequest(ctx, input)
		return input, hasPreferredStrictTools, nil
	}
	input, hasPreferredStrictTools, err := buildInput(false)
	if err != nil {
		return nil, err
	}
	converse := p.converseStream
	if converse == nil {
		converse = client.ConverseStream
	}
	// Pi returns the event stream before the request settles: the async body of `stream` starts at its first await (bedrock-converse-stream.ts:129-330).
	requestContext, abort := context.WithCancel(ctx)
	builder.SetAbort(abort)
	go builder.RunResponse(func() error {
		p.streamResponse(ctx, requestContext, converse, input, hasPreferredStrictTools, buildInput, opts, modelMeta, builder)
		return nil
	})
	return builder.Stream(), nil
}

// bedrockRequestBuilder builds the request again from the transcript, with every strict: "prefer" tool sent non-strict when disablePreferredStrictTools is set.
type bedrockRequestBuilder func(disablePreferredStrictTools bool) (input *bedrockruntime.ConverseStreamInput, hasPreferredStrictTools bool, err error)

// bedrockStrictToolLimitMessage returns the message of a send error that is a ValidationException saying the request's strict tools exceed Claude's limits.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:stream (error instanceof BedrockRuntimeServiceException, name ValidationException and isStrictToolLimitErrorMessage)
func bedrockStrictToolLimitMessage(err error) (string, bool) {
	apiError, ok := errors.AsType[smithy.APIError](err)
	if !ok || apiError.ErrorCode() != "ValidationException" {
		return "", false
	}
	message := apiError.ErrorMessage()
	return message, ai.IsStrictToolLimitErrorMessage(message)
}

// streamResponse sends the request and runs the stream loop. A rejection of the request's strict tool limits is sent once more with every strict: "prefer" tool non-strict: Bedrock checks the tool schemas before it starts the stream, so a retry never repeats streamed output. A body the transport can observe is read by the provider as an executor turn; any other body is read by the SDK's event-stream reader.
func (p *BedrockProvider) streamResponse(ctx, requestContext context.Context, converse func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error), input *bedrockruntime.ConverseStreamInput, hasPreferredStrictTools bool, buildInput bedrockRequestBuilder, opts ai.StreamOptions, modelMeta *ai.Model, builder *ai.AssistantStreamBuilder) {
	send := func(input *bedrockruntime.ConverseStreamInput) (*bedrockBodyTap, *bedrockruntime.ConverseStreamOutput, error) {
		tap := &bedrockBodyTap{}
		resp, err := converse(requestContext, input, tap.option, func(options *bedrockruntime.Options) {
			if opts.OnResponse != nil {
				options.APIOptions = append(options.APIOptions, func(stack *smithymiddleware.Stack) error {
					return stack.Deserialize.Add(smithymiddleware.DeserializeMiddlewareFunc("PiGProviderResponse", func(ctx context.Context, input smithymiddleware.DeserializeInput, next smithymiddleware.DeserializeHandler) (smithymiddleware.DeserializeOutput, smithymiddleware.Metadata, error) {
						output, metadata, err := next.HandleDeserialize(ctx, input)
						if err == nil {
							if response, ok := output.RawResponse.(*smithyhttp.Response); ok {
								err = ai.ObserveProviderResponse(ctx, opts, response.Response, modelMeta)
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
			options.APIOptions = append(options.APIOptions, bedrockUserAgent(opts.Identity), withBedrockHeaders(opts.Headers), withBedrockConverseBody(opts.RequestMetadataOrder))
		})
		return tap, resp, err
	}
	disablePreferredStrictTools := false
	var (
		tap  *bedrockBodyTap
		resp *bedrockruntime.ConverseStreamOutput
	)
	for {
		var err error
		tap, resp, err = send(input)
		if err == nil {
			break
		}
		limitMessage, strictToolLimit := bedrockStrictToolLimitMessage(err)
		if ctx.Err() != nil || disablePreferredStrictTools || !hasPreferredStrictTools || !strictToolLimit {
			failBedrockResponse(ctx, builder, tap.piError(bedrockSendError(ctx, err, p.baseURL, opts.Env)), "", false)
			tap.close()
			return
		}
		tap.close()
		disablePreferredStrictTools = true
		ai.AppendAssistantMessageDiagnostic(builder.Partial(), ai.AssistantMessageDiagnostic{
			Type:      "bedrock_strict_tools_fallback",
			Timestamp: time.Now().UnixMilli(),
			Error:     &ai.DiagnosticErrorInfo{Message: limitMessage},
			Details:   map[string]any{"reason": "strict_tool_limits_exceeded"},
		})
		if input, hasPreferredStrictTools, err = buildInput(true); err != nil {
			failBedrockResponse(ctx, builder, err, "", false)
			return
		}
	}
	defer tap.close()
	stream := resp.GetStream()
	if body := tap.body(); body != nil {
		if stream != nil {
			_ = stream.Close()
		}
		requestID, _ := awsmiddleware.GetRequestIDMetadata(resp.ResultMetadata)
		builder.SetManaged(true)
		_ = builder.ResponseTurn(func() error {
			p.runBedrockPipeline(ctx, builder.Turn(), builder, body, normalizeBedrockDiagnosticValue(requestID), opts.OnResponse != nil)
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

// bedrockClientOptions sets the runtime client's HTTP client (the only one that carries the Bedrock proxy), the JavaScript SDK's retry backoff in the client's resolved retry mode, and the bearer signer when the config has a bearer token.
func bedrockClientOptions(cfg aws.Config, httpClient bedrockruntime.HTTPClient) func(*bedrockruntime.Options) {
	return func(options *bedrockruntime.Options) {
		if httpClient != nil {
			options.HTTPClient = httpClient
		}
		options.Retryer = newBedrockRetryer(options.RetryMode)
		// pig divergence (D196): the HTTP-tolerant signer is installed only when loadBedrockConfig set a token, never under skip-auth.
		if cfg.BearerAuthTokenProvider != nil {
			options.BearerAuthSigner = bedrockBearerSigner{}
		}
	}
}

// loadBedrockConfig preserves explicit/scoped profile ownership, ambient key precedence, and ARN region precedence. A configured region or ambient profile unpins standard AWS endpoints; caller-supplied custom endpoints remain fixed.
//
// The proxy follows bedrock-converse-stream.ts: resolveHttpProxyUrlForTarget(model.baseUrl) is resolved once, and only the runtime client's
// requestHandler carries it. The credential clients keep the SDK's own handlers, which use no proxy: IMDS and the container provider call node:http
// directly, and the SSO client takes only the logger from its parent. The STS client inherits the parent's requestHandler only when that handler
// is not HTTP/2, which is when the proxy was found, so STS uses the runtime client's proxy and nothing else.
func loadBedrockConfig(ctx context.Context, baseURL, modelID string, options ai.StreamOptions) (aws.Config, *awshttp.BuildableClient, error) {
	env := options.Env
	proxyURL, proxyErr := ai.ResolveHTTPProxyURLForTarget(baseURL, env)
	runtimeHTTPClient := newBedrockHTTPClient(func(*http.Request) (*url.URL, error) { return proxyURL, proxyErr })
	credentialHTTPClient := newBedrockHTTPClient(nil)
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithHTTPClient(credentialHTTPClient)}
	if proxyURL != nil || proxyErr != nil {
		opts = append(opts,
			awsconfig.WithAssumeRoleCredentialOptions(func(o *stscreds.AssumeRoleOptions) {
				if client, ok := o.Client.(*sts.Client); ok {
					o.Client = bedrockProxiedSTS{client: client, httpClient: runtimeHTTPClient}
				}
			}),
			awsconfig.WithWebIdentityRoleCredentialOptions(func(o *stscreds.WebIdentityRoleOptions) {
				if client, ok := o.Client.(*sts.Client); ok {
					o.Client = bedrockProxiedSTS{client: client, httpClient: runtimeHTTPClient}
				}
			}),
		)
	}

	configuredRegion := ai.FirstNonEmptyString(options.Region, ai.GetProviderEnvValue("AWS_REGION", env), ai.GetProviderEnvValue("AWS_DEFAULT_REGION", env))
	ambientProfile := ai.GetProviderEnvValue("AWS_PROFILE", nil)
	useEndpoint := bedrockEndpointRegion(baseURL) == "" || (configuredRegion == "" && ambientProfile == "")
	regionEnv := env
	if options.Region != "" {
		regionEnv = ai.MergeProviderEnv(env, ai.ProviderEnv{"AWS_REGION": options.Region})
	}
	region := resolveBedrockRegionWithEnv(baseURL, modelID, regionEnv)
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	optionsProfile := ai.FirstNonEmptyString(options.Profile, env["AWS_PROFILE"])
	if profile := ai.FirstNonEmptyString(optionsProfile, ambientProfile); profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:stream
	// Explicit/scoped profiles own credential selection; ambient profiles do not displace env keys.
	skipAuth := ai.GetProviderEnvValue("AWS_BEDROCK_SKIP_AUTH", env) == "1"
	if skipAuth {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			"dummy-access-key", "dummy-secret-key", "",
		)))
	} else if accessKey, secretKey := ai.GetProviderEnvValue("AWS_ACCESS_KEY_ID", env), ai.GetProviderEnvValue("AWS_SECRET_ACCESS_KEY", env); optionsProfile == "" && accessKey != "" && secretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, ai.GetProviderEnvValue("AWS_SESSION_TOKEN", env))))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, nil, fmt.Errorf("load AWS config: %w", err)
	}
	// config.endpoint belongs to the BedrockRuntimeClient alone. It is set after loading, because the credential providers' STS, SSO and IMDS
	// clients are built from the configuration while it loads.
	if trimmed := strings.TrimSpace(baseURL); useEndpoint && trimmed != "" {
		cfg.BaseEndpoint = aws.String(strings.TrimRight(trimmed, "/"))
	}
	// pig divergence (D195): an empty AWS_BEARER_TOKEN_BEDROCK is no token, as in Pi's own provider-env lookup (getProviderEnvValue), so the request is signed with the
	// credentials found. Pi's AWS SDK selects bearer auth for any variable that is present, empty or not, and the request fails signing before it is sent.
	// pig divergence (D196): under skip-auth no bearer signer is installed here, so a token in the process environment, which the Go SDK picks up itself, keeps the SDK's
	// HTTPS-only signer; Pi's SDK sends the Authorization header over http://.
	if token := ai.FirstNonEmptyString(options.BearerToken, options.APIKey, ai.GetProviderEnvValue("AWS_BEARER_TOKEN_BEDROCK", env)); token != "" && !skipAuth {
		cfg.BearerAuthTokenProvider = staticBearerTokenProvider(token)
		cfg.AuthSchemePreference = []string{"httpBearerAuth"}
	}
	return cfg, runtimeHTTPClient, nil
}

// newBedrockHTTPClient builds the SDK's client with the given proxy function; a nil function means no proxy, as Node's handlers without an agent.
func newBedrockHTTPClient(proxy func(*http.Request) (*url.URL, error)) *awshttp.BuildableClient {
	return awshttp.NewBuildableClient().WithTransportOptions(func(transport *http.Transport) {
		// pig additive (D191): Bedrock, STS, SSO and the other credential clients share this builder.
		tlsdistrust.HardenTransport(transport)
		transport.Proxy = proxy
		if transport.DialContext != nil {
			transport.DialContext = bedrockObservedDial(transport.DialContext)
		}
	})
}

// bedrockProxiedSTS sends the STS calls of an assumed-role or web-identity credential through the runtime client's proxy.
type bedrockProxiedSTS struct {
	client     *sts.Client
	httpClient sts.HTTPClient
}

func (c bedrockProxiedSTS) useHTTPClient(options *sts.Options) { options.HTTPClient = c.httpClient }

func (c bedrockProxiedSTS) AssumeRole(ctx context.Context, input *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return c.client.AssumeRole(ctx, input, append(optFns, c.useHTTPClient)...)
}

func (c bedrockProxiedSTS) AssumeRoleWithWebIdentity(ctx context.Context, input *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	return c.client.AssumeRoleWithWebIdentity(ctx, input, append(optFns, c.useHTTPClient)...)
}

// bedrockARNRegionRE extracts the region embedded in an inference-profile ARN
// (e.g. arn:aws:bedrock:us-west-2:...). Mirrors upstream amazon-bedrock.ts:146.
var bedrockARNRegionRE = lazyregexp.New(`^arn:aws(?:-[a-z0-9-]+)?:bedrock:([a-z0-9-]+):`)

func resolveBedrockRegion(baseURL, modelID string) string {
	return resolveBedrockRegionWithEnv(baseURL, modelID, nil)
}

func resolveBedrockRegionWithEnv(baseURL, modelID string, env ai.ProviderEnv) string {
	// An inference-profile ARN's embedded region wins over AWS_REGION and the
	// rest of the chain (avoids conflicts with AWS_REGION set for other services).
	if m := bedrockARNRegionRE.FindStringSubmatch(modelID); m != nil {
		return m[1]
	}
	if r := ai.GetProviderEnvValue("AWS_REGION", env); r != "" {
		return r
	}
	if r := ai.GetProviderEnvValue("AWS_DEFAULT_REGION", env); r != "" {
		return r
	}
	if ai.GetProviderEnvValue("AWS_PROFILE", nil) != "" {
		return ""
	}
	if r := bedrockEndpointRegion(baseURL); r != "" {
		return r
	}
	return "us-east-1"
}

var bedrockEndpointHostRE = lazyregexp.New(`^bedrock-runtime(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)

// bedrockEndpointRegion is getStandardBedrockEndpointRegion: the region of a standard Bedrock runtime host in the WHATWG hostname of baseURL, or "".
func bedrockEndpointRegion(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	u, err := nodeurl.ParseURL(baseURL)
	if err != nil {
		return ""
	}
	m := bedrockEndpointHostRE.FindStringSubmatch(strings.ToLower(u.Hostname))
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
	headers ai.ProviderHeaders
}

func (middleware bedrockHeadersMiddleware) ID() string { return "pi-ai-custom-headers" }

func isReservedBedrockHeader(name string) bool {
	name = jsstring.ToLower(name)
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

func withBedrockHeaders(headers ai.ProviderHeaders) func(*smithymiddleware.Stack) error {
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
func resolveBedrockCacheRetention(cacheRetention ai.CacheRetention, env ai.ProviderEnv) bedrockCacheRetention {
	if cacheRetention != "" {
		return bedrockCacheRetention(cacheRetention)
	}
	if ai.GetProviderEnvValue("PI_CACHE_RETENTION", env) == "long" {
		return bedrockCacheLong
	}
	return bedrockCacheShort
}

// bedrockModelMatchCandidates is getModelMatchCandidates: the lowercased model id and, when it is not empty, name, each followed by itself with every run of whitespace, "_", "." and ":" rewritten to one "-".
func bedrockModelMatchCandidates(modelID, modelName string) []string {
	values := []string{modelID}
	if modelName != "" {
		values = append(values, modelName)
	}
	out := make([]string, 0, len(values)*2)
	for _, value := range values {
		lower := jsstring.ToLower(value)
		out = append(out, lower, bedrockSeparatorRuns(lower))
	}
	return out
}

// bedrockSeparatorRuns is `text.replace(/[\s_.:]+/g, "-")`.
func bedrockSeparatorRuns(text string) string {
	var out strings.Builder
	inRun := false
	for _, r := range text {
		if r == '_' || r == '.' || r == ':' || jsstring.IsSpace(r) {
			if !inRun {
				out.WriteByte('-')
			}
			inRun = true
			continue
		}
		inRun = false
		out.WriteRune(r)
	}
	return out.String()
}

// isAnthropicClaudeBedrockModel is isAnthropicClaudeModel: the lowercased id carries an anthropic.claude or anthropic/claude prefix, or the lowercased name carries one or the word claude.
func isAnthropicClaudeBedrockModel(modelID, modelName string) bool {
	id, name := jsstring.ToLower(modelID), jsstring.ToLower(modelName)
	return strings.Contains(id, "anthropic.claude") || strings.Contains(id, "anthropic/claude") || strings.Contains(name, "anthropic.claude") || strings.Contains(name, "anthropic/claude") || strings.Contains(name, "claude")
}

// supportsBedrockPromptCaching checks the model and request-scoped force opt-in before falling back to the process environment.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:supportsPromptCaching
func supportsBedrockPromptCaching(modelID, modelName string, env ai.ProviderEnv) bool {
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
		return ai.GetProviderEnvValue("AWS_BEDROCK_FORCE_CACHE", env) == "1"
	}
	for _, candidate := range candidates {
		if strings.Contains(candidate, "fable-5") || strings.Contains(candidate, "opus-5") || strings.Contains(candidate, "sonnet-5") || strings.Contains(candidate, "haiku-5") {
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
		if strings.Contains(candidate, "opus-4-6") || strings.Contains(candidate, "opus-4-7") || strings.Contains(candidate, "opus-4-8") || strings.Contains(candidate, "opus-5") || strings.Contains(candidate, "sonnet-4-6") || strings.Contains(candidate, "sonnet-5") || strings.Contains(candidate, "haiku-5") || strings.Contains(candidate, "fable-5") {
			return true
		}
	}
	return false
}

// ─── Message conversion ──────────────────────────────────────────────────

func buildBedrockSystemPrompt(modelID, modelName, systemPrompt string, cacheRetention bedrockCacheRetention, env ai.ProviderEnv) []btypes.SystemContentBlock {
	if systemPrompt == "" {
		return nil
	}
	blocks := []btypes.SystemContentBlock{
		&btypes.SystemContentBlockMemberText{Value: ai.SanitizeSurrogates(systemPrompt)},
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
func convertBedrockMessages(msgs []ai.Message, modelID, modelName string, cacheRetention bedrockCacheRetention, env ai.ProviderEnv) ([]btypes.Message, error) {
	out := make([]btypes.Message, 0, len(msgs))
	// pig divergence (D116): Pi pushes every user message and run of tool results as its own message, so two user messages can sit side by side (a prompt after tool results, a dropped assistant turn); Converse requires alternating roles, so adjacent messages of one role share a message.
	push := func(role btypes.ConversationRole, blocks []btypes.ContentBlock) {
		if last := len(out) - 1; last >= 0 && out[last].Role == role {
			out[last].Content = append(out[last].Content, blocks...)
			return
		}
		out = append(out, btypes.Message{Role: role, Content: blocks})
	}

	for i := 0; i < len(msgs); i++ {
		switch message := msgs[i].(type) {
		case ai.UserMessage:
			blocks, err := bedrockUserContent(message.Content)
			if err != nil {
				return nil, err
			}
			if len(blocks) == 0 {
				continue
			}
			push(btypes.ConversationRoleUser, blocks)

		case ai.AssistantMessage:
			blocks, err := bedrockAssistantContent(message.Content, modelID, modelName)
			if err != nil {
				return nil, err
			}
			if len(blocks) == 0 {
				// Mirrors upstream: skip empty assistant messages.
				continue
			}
			push(btypes.ConversationRoleAssistant, blocks)

		case ai.ToolResultMessage:
			// Collect this tool-result + all immediately following
			// tool-result messages into a single user message, exactly
			// like upstream's toolResult coalescing.
			combined, consumed, err := bedrockToolResultRun(msgs, i, isAnthropicClaudeBedrockModel(modelID, modelName))
			if err != nil {
				return nil, err
			}
			if len(combined) > 0 {
				push(btypes.ConversationRoleUser, combined)
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

func bedrockUserContent(content ai.UserContent) ([]btypes.ContentBlock, error) {
	switch content := content.(type) {
	case ai.UserText:
		text := ai.SanitizeSurrogates(string(content))
		if ai.TrimJSWhitespace(text) == "" {
			text = bedrockEmptyPlaceholder
		}
		return []btypes.ContentBlock{
			&btypes.ContentBlockMemberText{Value: text},
		}, nil
	case ai.UserContentBlocks:
		out := make([]btypes.ContentBlock, 0, len(content))
		for _, block := range content {
			switch block := block.(type) {
			case ai.TextContent:
				text := ai.SanitizeSurrogates(block.Text)
				if ai.TrimJSWhitespace(text) == "" {
					continue
				}
				out = append(out, &btypes.ContentBlockMemberText{Value: text})
			case ai.ImageContent:
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

func bedrockAssistantContent(blocks []ai.AssistantContentBlock, modelID, modelName string) ([]btypes.ContentBlock, error) {
	out := make([]btypes.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch block := block.(type) {
		case ai.TextContent:
			text := ai.SanitizeSurrogates(block.Text)
			if ai.TrimJSWhitespace(text) == "" {
				continue
			}
			out = append(out, &btypes.ContentBlockMemberText{Value: text})
		case ai.ToolCall:
			// Pi: input = sanitizeBedrockDocument(c.arguments), a non-object value included (bedrock-converse-stream.ts:1035).
			arguments, err := sanitizeBedrockDocument(block.ArgumentsValue())
			if err != nil {
				return nil, err
			}
			if arguments == nil && block.NonObjectArguments() == nil {
				arguments = map[string]any{}
			}
			encoded, err := ai.MarshalInRecordedOrder(arguments, block.ArgumentOrder())
			if err != nil {
				return nil, err
			}
			out = append(out, &btypes.ContentBlockMemberToolUse{Value: btypes.ToolUseBlock{
				ToolUseId: aws.String(block.ID),
				Name:      aws.String(block.Name),
				Input:     newBedrockDocument(arguments, encoded),
			}})
		case ai.ThinkingContent:
			// upstream: packages/ai/src/api/bedrock-converse-stream.ts:convertMessages
			if block.Redacted {
				if data := decodeBedrockRedactedContent(block.ThinkingSignature); len(data) > 0 {
					out = append(out, &btypes.ContentBlockMemberReasoningContent{
						Value: &btypes.ReasoningContentBlockMemberRedactedContent{Value: data},
					})
				}
				continue
			}
			thinking := ai.SanitizeSurrogates(block.Thinking)
			if ai.TrimJSWhitespace(thinking) == "" {
				continue
			}
			if supportsBedrockThinkingSignatureWithName(modelID, modelName) {
				if ai.TrimJSWhitespace(block.ThinkingSignature) == "" {
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
	normalized, err := ai.NormalizeJSONValue(value)
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
// of them, plus the number of messages consumed. Claude rejects error tool
// results that contain anything but text, so for Claude the images of an error
// result follow all tool results of the turn under a caption naming the call
// (Pi #10794, bedrock-converse-stream.ts convertMessages); other models keep
// them in place.
func bedrockToolResultRun(msgs []ai.Message, i int, claude bool) ([]btypes.ContentBlock, int, error) {
	out := []btypes.ContentBlock{}
	var errorImages []btypes.ContentBlock
	j := i
	for j < len(msgs) {
		result, ok := msgs[j].(ai.ToolResultMessage)
		if !ok {
			break
		}
		content := result.Content
		var split *ai.SplitErrorToolResult
		if claude {
			split = ai.SplitErrorToolResultImages(result)
		}
		if split != nil {
			content = make([]ai.ToolResultMessageContent, len(split.Content))
			for index, block := range split.Content {
				content[index] = block
			}
		}
		block, err := bedrockToolResultBlock(result, content)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, block)
		if split != nil {
			errorImages = append(errorImages, &btypes.ContentBlockMemberText{Value: split.Caption})
			for _, image := range split.Images {
				imageBlock, err := bedrockImageBlock(image.MimeType, image.Data)
				if err != nil {
					return nil, 0, err
				}
				errorImages = append(errorImages, &btypes.ContentBlockMemberImage{Value: imageBlock})
			}
		}
		j++
	}
	return append(out, errorImages...), j - i, nil
}

// bedrockToolResultBlock is the toolResult block of message with the given content, which is message.Content unless
// the caller split an error result's images out.
func bedrockToolResultBlock(message ai.ToolResultMessage, content []ai.ToolResultMessageContent) (btypes.ContentBlock, error) {
	var parts []btypes.ToolResultContentBlock
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			if text := ai.SanitizeSurrogates(block.Text); ai.TrimJSWhitespace(text) != "" {
				parts = append(parts, &btypes.ToolResultContentBlockMemberText{Value: text})
			}
		case ai.ImageContent:
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
		ToolUseId: aws.String(message.ToolCallID),
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

// convertBedrockTools is upstream convertToolConfig. disablePreferredStrictTools sends every tool whose constrainedSampling prefers strict without strict, for the
// strict tool fallback; hasPreferredStrictTools reports that a strict: "prefer" tool was sent strict.
func convertBedrockTools(tools []ai.ToolSchema, toolChoice any, supportsStrictMode, disablePreferredStrictTools bool) (config *btypes.ToolConfiguration, hasPreferredStrictTools bool, err error) {
	if len(tools) == 0 {
		return nil, false, nil
	}
	switch value := toolChoice.(type) {
	case nil, string, map[string]any:
	case ai.JsonObject:
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
		value, err := ai.NormalizeJSONValue(toolChoice)
		if err != nil {
			return nil, false, err
		}
		toolChoice = value
	}
	choice, _ := ai.ToolChoiceName(toolChoice)
	if choice == "none" {
		return nil, false, nil
	}
	out := make([]btypes.Tool, 0, len(tools))
	for _, tool := range tools {
		preferred := ai.PrefersJSONSchemaStrictSampling(tool)
		var strict *bool
		if !disablePreferredStrictTools || !preferred {
			var err error
			if strict, err = ai.ResolveJSONSchemaStrictSampling(tool, supportsStrictMode, nil); err != nil {
				return nil, false, err
			}
		}
		if strict != nil && *strict && preferred {
			hasPreferredStrictTools = true
		}
		toolParameters, err := ai.GetJSONSchemaToolParameters(tool, strict)
		if err != nil {
			return nil, false, err
		}
		parameters := toolParameters.Schema
		if parameters == nil {
			parameters = map[string]any{}
		}
		schema, err := ai.MarshalToolParameters(parameters, toolParameters.Order())
		if err != nil {
			return nil, false, err
		}
		toolSpec := btypes.ToolSpecification{
			Name:        aws.String(tool.Name),
			Description: aws.String(tool.Description),
			InputSchema: &btypes.ToolInputSchemaMemberJson{Value: newBedrockDocument(parameters, schema)},
		}
		if strict != nil && *strict {
			toolSpec.Strict = new(true)
		}
		out = append(out, &btypes.ToolMemberToolSpec{Value: toolSpec})
	}
	config = &btypes.ToolConfiguration{Tools: out}
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
				return nil, false, fmt.Errorf("toolChoice tool name must be a string")
			}
			config.ToolChoice = &btypes.ToolChoiceMemberTool{Value: btypes.SpecificToolChoice{Name: aws.String(name)}}
		}
	}
	return config, hasPreferredStrictTools, nil
}

// ─── Additional model request fields (thinking) ──────────────────────────

// isGovCloudBedrockTarget follows configured region precedence independently of endpoint and ARN region resolution.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:isGovCloudBedrockTarget
func isGovCloudBedrockTarget(model *ai.Model, opts ai.StreamOptions) bool {
	region := opts.Region
	if region == "" {
		region = ai.GetProviderEnvValue("AWS_REGION", opts.Env)
	}
	if region == "" {
		region = ai.GetProviderEnvValue("AWS_DEFAULT_REGION", opts.Env)
	}
	id := jsstring.ToLower(model.ID)
	return strings.HasPrefix(jsstring.ToLower(region), "us-gov-") || strings.HasPrefix(id, "us-gov.") || strings.HasPrefix(id, "arn:aws-us-gov:")
}

// buildBedrockAdditionalFields omits thinking.display for GovCloud targets in both adaptive and budget-based requests.
func buildBedrockAdditionalFields(model *ai.Model, modelName string, opts ai.StreamOptions) map[string]any {
	return buildBedrockAdditionalFieldsWithBudget(model, modelName, opts, nil)
}

// bedrockBudgetThinking reports whether the request uses budget-based Claude thinking.
func bedrockBudgetThinking(model *ai.Model, modelName string, opts ai.StreamOptions) bool {
	return opts.Thinking != "" && model != nil && isAnthropicClaudeBedrockModel(model.ID, modelName) && !supportsBedrockAdaptiveThinkingWithName(model.ID, modelName)
}

// adjustBedrockBudgetThinking is the budget-based branch of upstream streamSimple (bedrock-converse-stream.ts:552-575): the request's maxTokens becomes adjustMaxTokensForThinking's, clamped to the context, and the thinking budget is capped to leave 1024 tokens of answer room. It returns 0 and nil when the request does not use budget-based thinking or the model has no output cap.
func adjustBedrockBudgetThinking(model *ai.Model, modelName string, transcript ai.TranscriptContext, opts ai.StreamOptions) (maxTokens int, budget *int) {
	if !bedrockBudgetThinking(model, modelName, opts) || model.Capabilities.MaxOutputTokens <= 0 {
		return 0, nil
	}
	adjustedMax, adjustedBudget := ai.AdjustMaxTokensForThinking(maxTokensPtr(opts.MaxTokens), model.Capabilities.MaxOutputTokens, string(ai.ModelThinkingLevel(opts.Thinking)), opts.ThinkingBudgets)
	maxTokens = ai.ClampMaxTokensToContext(model, transcript, adjustedMax)
	return maxTokens, new(min(adjustedBudget, max(0, maxTokens-ai.MinAnswerTokens)))
}

// bedrockAdditionalFieldsOrder is the member order buildAdditionalModelRequestFields creates (bedrock-converse-stream.ts:1279-1327); a
// request has a subset of these members.
var bedrockAdditionalFieldsOrder = ai.SchemaObjectOrder{
	"":          {"thinking", "output_config", "anthropic_beta", "reasoning_effort", "reasoning"},
	"/thinking": {"type", "budget_tokens", "display", "block_binding"},
}

func buildBedrockAdditionalFieldsWithBudget(model *ai.Model, modelName string, opts ai.StreamOptions, budgetOverride *int) map[string]any {
	if opts.Thinking == "" || !opts.IsReasoning {
		return nil
	}
	if model == nil {
		return nil
	}
	if !isAnthropicClaudeBedrockModel(model.ID, modelName) {
		return bedrockOpenAIReasoningFields(model, modelName, ai.ModelThinkingLevel(opts.Thinking))
	}

	thinking := map[string]any{}
	isGovCloud := isGovCloudBedrockTarget(model, opts)
	if !isGovCloud {
		thinking["display"] = string(cmp.Or(opts.ThinkingDisplay, ai.AnthropicThinkingDisplaySummarized))
	}

	if supportsBedrockAdaptiveThinkingWithName(model.ID, modelName) {
		thinking["type"] = "adaptive"
		result := map[string]any{
			"thinking":      thinking,
			"output_config": map[string]any{"effort": mapBedrockThinkingEffort(model, ai.ModelThinkingLevel(opts.Thinking))},
		}
		// Replayed signed thinking blocks are bound to the system prompt and tools they were
		// created with. Bedrock 400s on replay after either changes unless stale blocks are
		// dropped, matching the Anthropic provider. Skipped on GovCloud like display.
		if !isGovCloud && supportsBedrockThinkingBlockBinding(model.ID, modelName) {
			thinking["block_binding"] = map[string]any{"prefix_mismatch_behavior": "drop_block"}
			result["anthropic_beta"] = []string{ai.ThinkingBindingControlsBeta}
		}
		return result
	}

	// upstream: bedrock-converse-stream.ts:buildAdditionalModelRequestFields: thinkingBudgets[level] ?? defaultBudgets[reasoning], with xhigh and max reading the high budget.
	var budget int
	switch {
	case budgetOverride != nil:
		budget = *budgetOverride
	default:
		level := ai.ClampReasoning(ai.ModelThinkingLevel(opts.Thinking))
		budget = ai.DefaultThinkingBudgets().High
		if level == ai.ThinkingMinimal || level == ai.ThinkingLow || level == ai.ThinkingMedium || level == ai.ThinkingHigh {
			budget = ai.ThinkingBudgetForLevel(string(level), opts.ThinkingBudgets)
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

// bedrockOpenAIReasoningFields is the reasoning request field of an OpenAI model on Bedrock: gpt-oss takes a flat reasoning_effort that only accepts low, medium and high; other GPT models (GPT-5.x, GPT-6) take a nested reasoning.effort and reject minimal. Other models get none (#9331).
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:buildAdditionalModelRequestFields, OPENAI_GPT_EFFORT, OPENAI_GPT_OSS_EFFORT
func bedrockOpenAIReasoningFields(model *ai.Model, modelName string, level ai.ModelThinkingLevel) map[string]any {
	candidates := bedrockModelMatchCandidates(model.ID, modelName)
	if slices.ContainsFunc(candidates, func(s string) bool { return strings.Contains(s, "gpt-oss") }) {
		effort := map[ai.ModelThinkingLevel]string{ai.ThinkingMinimal: "low", ai.ThinkingLow: "low", ai.ThinkingMedium: "medium", ai.ThinkingHigh: "high", ai.ThinkingXHigh: "high", ai.ThinkingMax: "high"}[level]
		return map[string]any{"reasoning_effort": effort}
	}
	if slices.ContainsFunc(candidates, func(s string) bool { return strings.Contains(s, "gpt-") }) {
		effort := map[ai.ModelThinkingLevel]string{ai.ThinkingMinimal: "low", ai.ThinkingLow: "low", ai.ThinkingMedium: "medium", ai.ThinkingHigh: "high", ai.ThinkingXHigh: "xhigh", ai.ThinkingMax: "max"}[level]
		if mapped := model.ThinkingLevelMap[level]; mapped != nil {
			effort = *mapped
		}
		return map[string]any{"reasoning": map[string]any{"effort": effort}}
	}
	return nil
}

// supportsBedrockThinkingBlockBinding reports whether the model accepts
// thinking.block_binding. Opus 4.6 and Sonnet 4.6 reject it with
// "thinking.adaptive.block_binding: Extra inputs are not permitted".
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:supportsThinkingBlockBinding
func supportsBedrockThinkingBlockBinding(modelID, modelName string) bool {
	for _, s := range bedrockModelMatchCandidates(modelID, modelName) {
		if strings.Contains(s, "opus-4-7") || strings.Contains(s, "opus-4-8") || strings.Contains(s, "opus-5") || strings.Contains(s, "sonnet-5") || strings.Contains(s, "haiku-5") || strings.Contains(s, "fable-5") {
			return true
		}
	}
	return false
}

// supportsNativeXhighEffort recognizes the Bedrock model families with native xhigh effort in both model IDs and display names.
// upstream: packages/ai/src/api/bedrock-converse-stream.ts:supportsNativeXhighEffort
func supportsNativeXhighEffort(model *ai.Model) bool {
	if model == nil {
		return false
	}
	for _, s := range bedrockModelMatchCandidates(model.ID, model.DisplayName) {
		if strings.Contains(s, "opus-4-7") || strings.Contains(s, "opus-4-8") || strings.Contains(s, "opus-5") || strings.Contains(s, "sonnet-5") || strings.Contains(s, "haiku-5") || strings.Contains(s, "fable-5") {
			return true
		}
	}
	return false
}

func mapBedrockThinkingEffort(model *ai.Model, level ai.ModelThinkingLevel) string {
	// upstream: packages/ai/src/api/bedrock-converse-stream.ts:mapThinkingLevelToEffort
	if level == ai.ThinkingXHigh && supportsNativeXhighEffort(model) {
		return "xhigh"
	}
	if model != nil {
		if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
			return *mapped
		}
	}
	switch level {
	case ai.ThinkingMinimal, ai.ThinkingLow:
		return "low"
	case ai.ThinkingMedium:
		return "medium"
	case ai.ThinkingHigh:
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

func flushBedrockRedactedContent(block *activeBlock, builder *ai.AssistantStreamBuilder) {
	if len(block.redactedContent) == 0 {
		return
	}
	thinking := builder.Partial().Content[block.contentIndex].(ai.ThinkingContent)
	thinking.ThinkingSignature = base64.StdEncoding.EncodeToString(block.redactedContent)
	thinking = thinking.WithSignatureEmpty(false)
	builder.Partial().Content[block.contentIndex] = thinking
	block.redactedContent = nil
}

type bedrockEventStream interface {
	Events() <-chan btypes.ConverseStreamOutput
	Close() error
	Err() error
}

func (p *BedrockProvider) parseBedrockStream(ctx context.Context, resp *bedrockruntime.ConverseStreamOutput, builder *ai.AssistantStreamBuilder) {
	stream := resp.GetStream()
	if stream == nil {
		builder.Fail(ai.StopReasonError, errors.New("amazon-bedrock: nil stream"))
		return
	}
	requestID, _ := awsmiddleware.GetRequestIDMetadata(resp.ResultMetadata)
	p.parseBedrockEvents(ctx, stream, builder, normalizeBedrockDiagnosticValue(requestID))
}

func (p *BedrockProvider) parseBedrockEvents(ctx context.Context, stream bedrockEventStream, builder *ai.AssistantStreamBuilder, requestID string) {
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
			err := builder.ObserveProviderEventData(ev) // bedrock-converse-stream.ts:297
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
func handleBedrockUsage(u *btypes.TokenUsage, builder *ai.AssistantStreamBuilder, usage *ai.Usage) {
	usage.Input = int(aws.ToInt32(u.InputTokens))
	usage.Output = int(aws.ToInt32(u.OutputTokens))
	usage.CacheRead = int(aws.ToInt32(u.CacheReadInputTokens))
	usage.CacheWrite = int(aws.ToInt32(u.CacheWriteInputTokens))
	usage.CacheWrite1h = nil
	// bedrock-converse-stream.ts:715 assigns cacheWrite1h after cost, undefined when there are no cache details.
	usage.SetSplitsAfterCost(true)
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
	builder.CalculateCost(usage)
	// output.usage is mutated in place (bedrock-converse-stream.ts:707-718), not replaced: earlier shallow copies observe the update.
	builder.Partial().Usage = *usage
}

func (p *BedrockProvider) handleBedrockDelta(ev btypes.ContentBlockDeltaEvent, blocks map[int32]*activeBlock, builder *ai.AssistantStreamBuilder) {
	p.handleBedrockDeltaFields(ev, nil, blocks, builder)
}

// handleBedrockDeltaFields is handleContentBlockDelta. reasoning holds every reasoningContent member of the payload when the caller decoded the JSON itself; nil reads the single member of the SDK's union value.
func (p *BedrockProvider) handleBedrockDeltaFields(ev btypes.ContentBlockDeltaEvent, reasoning *bedrockReasoningFields, blocks map[int32]*activeBlock, builder *ai.AssistantStreamBuilder) {
	idx := aws.ToInt32(ev.ContentBlockIndex)
	switch d := ev.Delta.(type) {
	case *btypes.ContentBlockDeltaMemberText:
		// Lazily create a text block if needed (Bedrock does not emit
		// ContentBlockStart for text blocks).
		block, ok := blocks[idx]
		if !ok {
			block = &activeBlock{kind: "text", contentIndex: builder.TextBlockStart()}
			blocks[idx] = block
		}
		if block.kind == "text" {
			builder.TextBlockDelta(block.contentIndex, d.Value)
		}

	case *btypes.ContentBlockDeltaMemberToolUse:
		b, ok := blocks[idx]
		if !ok || b.kind != "toolUse" {
			return
		}
		b.partialJSON.WriteString(aws.ToString(d.Value.Input))
		builder.ToolCallDelta(ai.NewStreamToolCallDelta(int(idx), b.toolID, b.toolName, aws.ToString(d.Value.Input)))

	case *btypes.ContentBlockDeltaMemberReasoningContent:
		block, ok := blocks[idx]
		if !ok {
			// upstream: packages/ai/src/api/bedrock-converse-stream.ts:handleContentBlockDelta
			block = &activeBlock{kind: "thinking", contentIndex: builder.ThinkingBlockStartWithContent(ai.ThinkingContent{}.WithSignatureEmpty(true))}
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
			builder.ThinkingBlockDelta(block.contentIndex, *reasoning.text)
		}
		if reasoning.signature != nil && *reasoning.signature != "" {
			thinking := builder.Partial().Content[block.contentIndex].(ai.ThinkingContent)
			if !thinking.Redacted {
				thinking.ThinkingSignature = builder.AppendSignature(block.contentIndex, thinking.ThinkingSignature, *reasoning.signature)
				thinking = thinking.WithSignatureEmpty(thinking.ThinkingSignature == "")
				builder.Partial().Content[block.contentIndex] = thinking
			}
		}
		if len(reasoning.redacted) > 0 {
			thinking := builder.Partial().Content[block.contentIndex].(ai.ThinkingContent)
			if !thinking.Redacted {
				thinking.Redacted = true
				thinking.ThinkingSignature = ""
				thinking = thinking.WithSignatureEmpty(true)
				builder.Partial().Content[block.contentIndex] = thinking
				builder.ThinkingBlockDelta(block.contentIndex, "[Reasoning redacted]")
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

func mapBedrockStopReason(reason string) (ai.StopReason, string) {
	switch reason {
	case string(btypes.StopReasonEndTurn), string(btypes.StopReasonStopSequence):
		return ai.StopReasonStop, ""
	case string(btypes.StopReasonMaxTokens), string(btypes.StopReasonModelContextWindowExceeded):
		return ai.StopReasonLength, ""
	case string(btypes.StopReasonToolUse):
		return ai.StopReasonToolUse, ""
	default:
		if reason != "" {
			return ai.StopReasonError, "Provider stopped with: " + reason
		}
		return ai.StopReasonError, ""
	}
}

// ─── Error formatting ────────────────────────────────────────────────────

// upstream: packages/ai/src/api/bedrock-converse-stream.ts:MAX_BEDROCK_DIAGNOSTIC_VALUE_CHARS
const maxBedrockDiagnosticValueChars = 200

func normalizeBedrockDiagnosticValue(value string) string {
	value = strings.TrimFunc(value, func(r rune) bool { return r == '\ufeff' || (r != '\u0085' && unicode.IsSpace(r)) })
	if value == "" || ai.Utf16Length(value) > maxBedrockDiagnosticValueChars {
		return ""
	}
	return value
}

func appendBedrockFailureDiagnostic(message *ai.AssistantMessage, err error, fallbackRequestID string) {
	details := ai.NewDiagnosticDetails()
	// The SDK wraps a transport failure in a ResponseError whose response was never received (status 0); Pi's SDK error then has no $metadata.httpStatusCode.
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && response.Response != nil && response.Response.Response != nil && response.HTTPStatusCode() != 0 {
		details.Set("status", response.HTTPStatusCode(), nil)
	}
	code := ""
	if apiError, ok := errors.AsType[smithy.APIError](err); ok {
		code = apiError.ErrorCode()
	}
	if strings.HasSuffix(code, "Exception") {
		if code = normalizeBedrockDiagnosticValue(code); code != "" {
			details.Set("errorCode", code, nil)
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
		details.Set("requestId", requestID, nil)
	}
	if details.Empty() {
		return
	}
	ai.AppendAssistantMessageDiagnostic(message, details.Attach(ai.AssistantMessageDiagnostic{Type: "bedrock_response_failure", Timestamp: time.Now().UnixMilli()}))
}

func failBedrockResponse(ctx context.Context, builder *ai.AssistantStreamBuilder, err error, requestID string, streaming bool) {
	if ctx.Err() != nil {
		builder.Fail(ai.StopReasonAborted, errors.New("Request aborted"))
		return
	}
	appendBedrockFailureDiagnostic(builder.Partial(), err, requestID)
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
	builder.Fail(ai.StopReasonError, errors.New(message))
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
	if responseErr, ok := errors.AsType[*bedrockResponseError](err); ok {
		return responseErr.text
	}
	norm := ai.NormalizeProviderError(err)
	core := ai.FormatProviderError(norm)
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
	if strings.Contains(jsstring.ToLower(message), "data retention mode") {
		return " See https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html for supported data retention modes."
	}
	return ""
}

// mapBedrockTransportError applies the same categories at the AWS SDK boundary. Modeled HTTP/service errors remain untouched so status and Bedrock exception classification continue to control overflow and retry behavior.
func mapBedrockTransportError(ctx context.Context, err error, message string) error {
	if err == nil {
		return nil
	}
	if contextErr := ai.ContextTransportError(ctx); contextErr != nil {
		return contextErr
	}
	if _, mapped := errors.AsType[*ai.NodeTransportError](err); mapped || !ai.IsGoTransportError(err) {
		return err
	}
	return ai.NewNodeTransportError(message, err)
}

// bedrockSendError is the error Pi's AWS SDK client rejects with when the request could not be sent. Its Node handlers report a
// failed connect or lookup with Node's own error, not a fetch rejection. Through NodeHttpHandler (an HTTP proxy for the endpoint,
// or AWS_BEDROCK_FORCE_HTTP1=1; bedrock-converse-stream.ts:220-232) that is the net error itself, "connect ECONNREFUSED …", which
// retry.ts does not retry. The default NodeHttp2Handler cancels the pending stream with it as the cause, which retry.ts retries.
func bedrockSendError(ctx context.Context, err error, baseURL string, env ai.ProviderEnv) error {
	if ctx.Err() != nil || !ai.IsGoTransportError(err) {
		return err
	}
	reason, ok := ai.NodeNetErrorMessage(err)
	if !ok {
		return err
	}
	// Pi resolves the proxy for model.baseUrl itself; a model without one has no proxy target and keeps NodeHttp2Handler.
	if proxy, _ := ai.ResolveHTTPProxyURLForTarget(baseURL, env); proxy != nil || ai.GetProviderEnvValue("AWS_BEDROCK_FORCE_HTTP1", env) == "1" {
		return ai.NewNodeTransportError(reason, err)
	}
	return ai.NewNodeTransportError("The pending stream has been canceled (caused by: "+reason+")", err)
}

// awsResponseErrorStatus reports the HTTP status of an AWS SDK response error in err's chain.
func awsResponseErrorStatus(err error) (int, bool) {
	response, ok := errors.AsType[*smithyhttp.ResponseError](err)
	if !ok || response.HTTPStatusCode() == 0 {
		// A transport failure carries a ResponseError without a received response; Pi's SDK error has no status then.
		return 0, false
	}
	return response.HTTPStatusCode(), true
}

func init() {
	ai.RegisterOptionalAPI(ai.OptionalAPIRegistration{
		API: ai.APIBedrockConverseStream,
		New: func(model ai.Model) (ai.Provider, error) {
			return NewBedrockProviderWithModel(model), nil
		},
		Factory: func(_, model, baseURL string) ai.Provider {
			return NewBedrockProvider(model, baseURL)
		},
		ResponseErrorStatus: awsResponseErrorStatus,
	})
}

// bedrockUserAgent is the client identity as AWS SDK user agent pairs (bedrock-converse-stream.ts bedrockUserAgent): the SDK appends them to its own User-Agent, so the detailed form is reduced to name and version.
//
// pig divergence (D65): the name is PiG's product name, and the AWS SDK for Go writes the pairs into its own User-Agent. A custom string is appended after the SDK's own value with the JavaScript SDK's escaping (each slash-separated part keeps its slashes and replaces characters outside the token set with "-"), because the Go SDK's own key helper would also replace the slash.
func bedrockUserAgent(identity *ai.ClientIdentity) func(*smithymiddleware.Stack) error {
	mode := ai.UserAgentDetailed
	if identity != nil && identity.UserAgent != nil {
		mode = *identity.UserAgent
	}
	if custom, ok := mode.Custom(); ok {
		return appendBedrockUserAgent(escapeBedrockUserAgent(custom))
	}
	if mode == ai.UserAgentShort {
		return awsmiddleware.AddUserAgentKey(pigidentity.UserAgentProduct)
	}
	return awsmiddleware.AddUserAgentKeyValue(pigidentity.UserAgentProduct, ai.GetClientVersion(identity))
}

// escapeBedrockUserAgent replaces each character outside the HTTP token set, per UTF-16 unit as the JavaScript SDK does, with "-".
func escapeBedrockUserAgent(value string) string {
	var escaped strings.Builder
	for _, r := range value {
		switch {
		case r == '/' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || strings.ContainsRune("!#$%&'*+-.^_`|~", r):
			escaped.WriteRune(r)
		case r > 0xFFFF:
			escaped.WriteString("--")
		default:
			escaped.WriteByte('-')
		}
	}
	return escaped.String()
}

// appendBedrockUserAgent adds value to the User-Agent the SDK's own middleware builds, so it must run after that middleware.
func appendBedrockUserAgent(value string) func(*smithymiddleware.Stack) error {
	return func(stack *smithymiddleware.Stack) error {
		return stack.Build.Insert(smithymiddleware.BuildMiddlewareFunc("pig-client-identity-user-agent", func(ctx context.Context, in smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
			if request, ok := in.Request.(*smithyhttp.Request); ok {
				request.Header.Set("User-Agent", strings.TrimSpace(request.Header.Get("User-Agent")+" "+value))
			}
			return next.HandleBuild(ctx, in)
		}), (&awsmiddleware.RequestUserAgent{}).ID(), smithymiddleware.After)
	}
}
