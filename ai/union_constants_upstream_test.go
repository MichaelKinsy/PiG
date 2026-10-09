package ai

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// upstreamStringUnion returns the string literals of `export type <name> = ...;` in a pinned upstream source file, in order.
func upstreamStringUnion(t *testing.T, file, name string) []string {
	t.Helper()
	data, err := os.ReadFile("../.upstream/current/packages/ai/src/" + file)
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`(?s)export type ` + name + ` =(.*?);`).FindStringSubmatch(string(data))
	if declaration == nil {
		t.Fatalf("%s: no `export type %s`", file, name)
	}
	var values []string
	for _, match := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(declaration[1], -1) {
		values = append(values, match[1])
	}
	return values
}

// The ledger closes an upstream string-union alias only when the Go named string type declares the same value set.
func TestUpstreamStringUnionsMatchTheGoConstants(t *testing.T) {
	for _, tc := range []struct {
		upstream, file string
		goValues       []string
	}{
		{"StopReason", "types.ts", []string{string(StopReasonPending), string(StopReasonStop), string(StopReasonLength), string(StopReasonToolUse), string(StopReasonError), string(StopReasonAborted), string(StopReasonDeferred)}},
		{"CacheRetention", "types.ts", []string{string(CacheRetentionNone), string(CacheRetentionShort), string(CacheRetentionLong)}},
		{"Transport", "types.ts", []string{string(TransportSSE), string(TransportWebSocket), string(TransportWebSocketCached), string(TransportAuto)}},
		{"SessionAffinityFormat", "types.ts", []string{string(SessionAffinityOpenAI), string(SessionAffinityOpenAINoSession), string(SessionAffinityOpenRouter)}},
		{"ImagesStopReason", "types.ts", []string{string(ImagesStopReasonStop), string(ImagesStopReasonError), string(ImagesStopReasonAborted)}},
		{"ClassifierStopReason", "types.ts", []string{string(ClassifierStopReasonStop), string(ClassifierStopReasonError), string(ClassifierStopReasonAborted)}},
		{"ModelsErrorCode", "utils/models-error.ts", []string{string(ModelsErrorModelSource), string(ModelsErrorModelValidation), string(ModelsErrorProvider), string(ModelsErrorStream), string(ModelsErrorAuth), string(ModelsErrorOAuth)}},
		{"AuthType", "auth/types.ts", []string{string(CredentialAPIKey), string(CredentialOAuth)}},
		{"KnownApi", "types.ts", []string{string(APIOpenAICompletions), string(APIMistralConversations), string(APIOpenAIResponses), string(APIAzureOpenAIResponses), string(APIOpenAICodexResponses), string(APIAnthropicMessages), string(APIBedrockConverseStream), string(APIGoogleGenerativeAI), string(APIGoogleVertex), string(APIPiMessages)}},
		{"GoogleApiThinkingLevel", "api/google-shared.ts", []string{string(GoogleThinkingLevelUnspecified), string(GoogleThinkingLevelMinimal), string(GoogleThinkingLevelLow), string(GoogleThinkingLevelMedium), string(GoogleThinkingLevelHigh)}},
		{"KnownImageApi", "types.ts", []string{string(APIImagesOpenRouter)}},
		{"KnownClassifierApi", "types.ts", []string{string(ClassifierAPITypesafeSystemOne), string(ClassifierAPICloudflareWorkersAISystemOne), string(ClassifierAPILlamaCppClassify), string(ClassifierAPIOpenAIDecisions)}},
	} {
		if want := upstreamStringUnion(t, tc.file, tc.upstream); !slices.Equal(want, tc.goValues) {
			t.Errorf("%s: Go constants %v, upstream %v", tc.upstream, tc.goValues, want)
		}
	}
	// ModelThinkingLevel is "off" followed by every ModelThinkingLevel.
	want := append([]string{"off"}, upstreamStringUnion(t, "types.ts", "ThinkingLevel")...)
	got := []string{string(ThinkingOff), string(ThinkingMinimal), string(ThinkingLow), string(ThinkingMedium), string(ThinkingHigh), string(ThinkingXHigh), string(ThinkingMax)}
	if !slices.Equal(got, want) {
		t.Errorf("ModelThinkingLevel: Go constants %v, upstream %v", got, want)
	}
	// ModelType is keyof ModelTypeMap.
	data, err := os.ReadFile("../.upstream/current/packages/ai/src/types.ts")
	if err != nil {
		t.Fatal(err)
	}
	mapBody := regexp.MustCompile(`(?s)export interface ModelTypeMap \{(.*?)\n\}`).FindStringSubmatch(string(data))
	if mapBody == nil {
		t.Fatal("ModelTypeMap not found")
	}
	var keys []string
	for line := range strings.SplitSeq(mapBody[1], "\n") {
		if key, _, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && regexp.MustCompile(`^\w+$`).MatchString(key) {
			keys = append(keys, key)
		}
	}
	if goTypes := []string{string(ModelTypeChat), string(ModelTypeImage), string(ModelTypeClassifier)}; !slices.Equal(goTypes, keys) {
		t.Errorf("ModelType: Go constants %v, upstream %v", goTypes, keys)
	}
}

// upstreamDiscriminators returns the `type: "<literal>"` values of the object members of `export type <name> = ...;`, in order.
func upstreamDiscriminators(t *testing.T, file, name string) []string {
	t.Helper()
	data, err := os.ReadFile("../.upstream/current/packages/ai/src/" + file)
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`(?s)export type ` + name + ` =(.*?)\n\n`).FindStringSubmatch(string(data))
	if declaration == nil {
		t.Fatalf("%s: no `export type %s`", file, name)
	}
	var values []string
	for _, match := range regexp.MustCompile(`\btype: "([^"]+)"`).FindAllStringSubmatch(declaration[1], -1) {
		values = append(values, match[1])
	}
	return values
}

// A tagged-struct union's discriminator is a named Go string type whose constants are the union's literals, so the compiler rejects a value outside the closed set that a bare string accepts.
func TestDiscriminatedUnionTypesMatchTheGoConstants(t *testing.T) {
	for _, tc := range []struct {
		upstream, file string
		goValues       []string
	}{
		{"PiMessagesEvent", "api/pi-messages.ts", []string{string(PiMessagesEventStart), string(PiMessagesEventTextStart), string(PiMessagesEventTextDelta), string(PiMessagesEventTextEnd), string(PiMessagesEventThinkingStart), string(PiMessagesEventThinkingDelta), string(PiMessagesEventThinkingEnd), string(PiMessagesEventToolcallStart), string(PiMessagesEventToolcallDelta), string(PiMessagesEventToolcallEnd), string(PiMessagesEventDone), string(PiMessagesEventError)}},
		{"ConstrainedSamplingConfig", "types.ts", []string{string(ConstrainedSamplingJSONSchema), string(ConstrainedSamplingGrammar)}},
	} {
		if want := upstreamDiscriminators(t, tc.file, tc.upstream); !slices.Equal(want, tc.goValues) {
			t.Errorf("%s: Go constants %v, upstream %v", tc.upstream, tc.goValues, want)
		}
	}
}
