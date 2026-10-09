package codingagent

// Ports packages/coding-agent/src/core/model-config.ts: the models.json schema and the way ModelConfig.load reports a
// document that does not match it (typebox's localized validation errors, formatted by formatValidationPath).

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
)

type jsonKind uint8

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

// jsonNode is a parsed JSON value that keeps the order JavaScript enumerates an object's keys in: integer-like keys
// ascending, then the rest in first-insertion order.
type jsonNode struct {
	kind   jsonKind
	text   string
	number float64
	items  []jsonNode
	keys   []string
	fields map[string]jsonNode
}

func parseJSONNode(data []byte) (jsonNode, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return parseJSONValue(decoder)
}

func parseJSONValue(decoder *json.Decoder) (jsonNode, error) {
	token, err := decoder.Token()
	if err != nil {
		return jsonNode{}, err
	}
	switch value := token.(type) {
	case json.Delim:
		if value == '[' {
			node := jsonNode{kind: jsonArray}
			for decoder.More() {
				item, err := parseJSONValue(decoder)
				if err != nil {
					return jsonNode{}, err
				}
				node.items = append(node.items, item)
			}
			_, err := decoder.Token()
			return node, err
		}
		node := jsonNode{kind: jsonObject, fields: map[string]jsonNode{}}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return jsonNode{}, err
			}
			key := keyToken.(string)
			item, err := parseJSONValue(decoder)
			if err != nil {
				return jsonNode{}, err
			}
			if _, seen := node.fields[key]; !seen {
				node.keys = append(node.keys, key)
			}
			node.fields[key] = item
		}
		if _, err := decoder.Token(); err != nil {
			return jsonNode{}, err
		}
		node.keys = jsKeyOrder(node.keys)
		return node, nil
	case string:
		return jsonNode{kind: jsonString, text: value}, nil
	case json.Number:
		number, err := strconv.ParseFloat(string(value), 64)
		if err != nil && !math.IsInf(number, 0) {
			return jsonNode{}, err
		}
		return jsonNode{kind: jsonNumber, number: number}, nil
	case bool:
		return jsonNode{kind: jsonBool, number: map[bool]float64{true: 1}[value]}, nil
	}
	return jsonNode{kind: jsonNull}, nil
}

func arrayIndexKey(key string) (uint64, bool) {
	if key == "" || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	index, err := strconv.ParseUint(key, 10, 64)
	return index, err == nil && index < 1<<32-1
}

func jsKeyOrder(keys []string) []string {
	var indexes, rest []string
	for _, key := range keys {
		if _, ok := arrayIndexKey(key); ok {
			indexes = append(indexes, key)
		} else {
			rest = append(rest, key)
		}
	}
	slices.SortStableFunc(indexes, func(a, b string) int {
		x, _ := arrayIndexKey(a)
		y, _ := arrayIndexKey(b)
		return int(max(-1, min(1, int64(x)-int64(y))))
	})
	return append(indexes, rest...)
}

type schemaKind uint8

const (
	schemaString schemaKind = iota
	schemaNumber
	schemaInteger
	schemaBoolean
	schemaNull
	schemaLiteral
	schemaUnknown
	schemaArray
	schemaObject
	schemaRecord
	schemaUnion
)

type schemaProp struct {
	name     string
	schema   *schema
	optional bool
}

type schema struct {
	kind       schemaKind
	props      []schemaProp
	elem       *schema
	alts       []*schema
	literal    string
	minLength  int
	minimum    *float64
	maximum    *float64
	exclusiveM *float64
	maxItems   int
}

func sString(minLength int) *schema { return &schema{kind: schemaString, minLength: minLength} }
func sNumber() *schema              { return &schema{kind: schemaNumber} }
func sBoolean() *schema             { return &schema{kind: schemaBoolean} }
func sLiteral(v string) *schema     { return &schema{kind: schemaLiteral, literal: v} }
func sUnion(alts ...*schema) *schema {
	return &schema{kind: schemaUnion, alts: alts}
}
func sEnum(values ...string) *schema {
	alts := make([]*schema, len(values))
	for i, value := range values {
		alts[i] = sLiteral(value)
	}
	return sUnion(alts...)
}
func sArray(elem *schema) *schema  { return &schema{kind: schemaArray, elem: elem} }
func sRecord(elem *schema) *schema { return &schema{kind: schemaRecord, elem: elem} }
func sInteger(minimum, maximum float64) *schema {
	s := &schema{kind: schemaInteger, minimum: &minimum}
	if maximum != 0 {
		s.maximum = &maximum
	}
	return s
}
func sPositive() *schema {
	zero := 0.0
	return &schema{kind: schemaNumber, exclusiveM: &zero}
}
func sObject(props ...schemaProp) *schema   { return &schema{kind: schemaObject, props: props} }
func opt(name string, s *schema) schemaProp { return schemaProp{name: name, schema: s, optional: true} }
func req(name string, s *schema) schemaProp { return schemaProp{name: name, schema: s} }

var modelConfigSchema = buildModelConfigSchema()

func buildModelConfigSchema() *schema {
	str1 := func() *schema { return sString(1) }
	unknownRecord := &schema{kind: schemaRecord, elem: &schema{kind: schemaUnknown}}
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	levelProps := func(value func() *schema) []schemaProp {
		props := make([]schemaProp, len(levels))
		for i, level := range levels {
			props[i] = opt(level, value())
		}
		return props
	}
	percentile := sObject(opt("p50", sNumber()), opt("p75", sNumber()), opt("p90", sNumber()), opt("p99", sNumber()))
	numberOrString := func() *schema { return sUnion(sNumber(), sString(0)) }
	routing := sObject(
		opt("allow_fallbacks", sBoolean()), opt("require_parameters", sBoolean()), opt("data_collection", sEnum("deny", "allow")), opt("zdr", sBoolean()),
		opt("enforce_distillable_text", sBoolean()), opt("order", sArray(sString(0))), opt("only", sArray(sString(0))), opt("ignore", sArray(sString(0))), opt("quantizations", sArray(sString(0))),
		opt("sort", sUnion(sString(0), sObject(opt("by", sString(0)), opt("partition", sUnion(sString(0), &schema{kind: schemaNull}))))),
		opt("max_price", sObject(opt("prompt", numberOrString()), opt("completion", numberOrString()), opt("image", numberOrString()), opt("audio", numberOrString()), opt("request", numberOrString()))),
		opt("preferred_min_throughput", sUnion(sNumber(), percentile)), opt("preferred_max_latency", sUnion(sNumber(), percentile)),
	)
	vercel := sObject(opt("only", sArray(sString(0))), opt("order", sArray(sString(0))))
	thinkingValue := func() *schema { return sUnion(sString(0), &schema{kind: schemaNull}) }
	thinkingLevelMap := sObject(levelProps(thinkingValue)...)
	samplingByLevel := sObject(levelProps(func() *schema { return unknownRecord })...)
	kwargScalar := []*schema{sString(0), sNumber(), sBoolean(), {kind: schemaNull}}
	kwarg := sUnion(sUnion(kwargScalar...), sObject(req("$var", sEnum("thinking.enabled", "thinking.effort")), opt("omitWhenOff", sBoolean())))
	sessionAffinity := func() *schema { return sEnum("openai", "openai-nosession", "openrouter") }
	completionsCompat := sObject(
		opt("supportsStore", sBoolean()), opt("supportsDeveloperRole", sBoolean()), opt("supportsReasoningEffort", sBoolean()), opt("supportsUsageInStreaming", sBoolean()), opt("supportsFinishReason", sBoolean()),
		opt("maxTokensField", sEnum("max_completion_tokens", "max_tokens")), opt("requiresToolResultName", sBoolean()), opt("requiresAssistantAfterToolResult", sBoolean()), opt("requiresThinkingAsText", sBoolean()),
		opt("requiresReasoningContentOnAssistantMessages", sBoolean()),
		opt("thinkingFormat", sEnum("openai", "openrouter", "together", "baseten", "deepseek", "zai", "qwen", "chat-template", "qwen-chat-template", "string-thinking", "ant-ling")),
		opt("chatTemplateKwargs", sRecord(kwarg)), opt("chatTemplateArgs", sRecord(kwarg)), opt("cacheControlFormat", sLiteral("anthropic")), opt("openRouterRouting", routing), opt("vercelGatewayRouting", vercel),
		opt("supportsOpenAIGrammarTools", sBoolean()), opt("supportsStrictMode", sBoolean()), opt("sendSessionAffinityHeaders", sBoolean()), opt("sessionAffinityFormat", sessionAffinity()),
		opt("supportsLongCacheRetention", sBoolean()), opt("vllmPriority", sNumber()),
	)
	responsesCompat := sObject(
		opt("supportsDeveloperRole", sBoolean()), opt("sessionAffinityFormat", sessionAffinity()), opt("supportsLongCacheRetention", sBoolean()), opt("supportsStrictMode", sBoolean()),
		opt("supportsOpenAIGrammarTools", sBoolean()), opt("supportsMaxOutputTokens", sBoolean()),
	)
	costRates := func() []schemaProp {
		return []schemaProp{req("input", sNumber()), req("output", sNumber()), req("cacheRead", sNumber()), req("cacheWrite", sNumber())}
	}
	costTier := func() *schema {
		return sObject(append([]schemaProp{req("inputTokensAbove", sNumber())}, costRates()...)...)
	}
	cost := sObject(append(costRates(), opt("tiers", sArray(costTier())))...)
	promptCache := sObject(opt("short", sPositive()), opt("long", sPositive()))
	imageResize := sObject(opt("maxWidth", sInteger(1, 0)), opt("maxHeight", sInteger(1, 0)), opt("maxBytes", sInteger(1, 0)), opt("jpegQuality", sInteger(1, 100)))
	inputLimits := sObject(opt("maxRequestBytes", sInteger(1, 0)), opt("images", sObject(opt("resize", imageResize), opt("maxPerMessage", sInteger(1, 0)), opt("maxPerRequest", sInteger(1, 0)))))
	fallbackModels := sArray(sObject(req("provider", str1()), req("model", str1()), req("cost", cost)))
	// upstream: packages/coding-agent/src/core/model-config.ts:maxItems
	fallbackModels.maxItems = 3
	anthropicCompat := sObject(
		opt("supportsEagerToolInputStreaming", sBoolean()), opt("supportsLongCacheRetention", sBoolean()), opt("sendSessionAffinityHeaders", sBoolean()), opt("supportsCacheControlOnTools", sBoolean()),
		opt("supportsTemperature", sBoolean()), opt("forceAdaptiveThinking", sBoolean()), opt("allowEmptySignature", sBoolean()), opt("supportsStrictTools", sBoolean()), opt("supportsMidConvoEffort", sBoolean()),
		opt("allowedFallbackModels", fallbackModels),
	)
	compat := func() *schema { return sUnion(completionsCompat, responsesCompat, anthropicCompat) }
	headers := func() *schema { return sRecord(sString(0)) }
	input := func() *schema { return sArray(sEnum("text", "image")) }
	model := sObject(
		req("id", str1()), opt("name", str1()), opt("api", str1()), opt("baseUrl", str1()), opt("reasoning", sBoolean()), opt("thinkingLevelMap", thinkingLevelMap), opt("input", input()), opt("inputLimits", inputLimits),
		opt("cost", cost), opt("promptCache", promptCache), opt("contextWindow", sNumber()), opt("maxTokens", sNumber()), opt("samplingParams", unknownRecord), opt("samplingParamsByThinkingLevel", samplingByLevel),
		opt("headers", headers()), opt("compat", compat()),
	)
	override := sObject(
		opt("name", str1()), opt("reasoning", sBoolean()), opt("thinkingLevelMap", thinkingLevelMap), opt("input", input()), opt("inputLimits", inputLimits),
		opt("cost", sObject(opt("input", sNumber()), opt("output", sNumber()), opt("cacheRead", sNumber()), opt("cacheWrite", sNumber()), opt("tiers", sArray(costTier())))),
		opt("promptCache", promptCache), opt("contextWindow", sNumber()), opt("maxTokens", sNumber()), opt("samplingParams", unknownRecord), opt("samplingParamsByThinkingLevel", samplingByLevel),
		opt("headers", headers()), opt("compat", compat()),
	)
	provider := sObject(
		opt("name", str1()), opt("baseUrl", str1()), opt("apiKey", str1()), opt("api", str1()), opt("oauth", sLiteral("radius")), opt("headers", headers()), opt("compat", compat()),
		opt("authHeader", sBoolean()), opt("models", sArray(model)), opt("modelOverrides", sRecord(override)),
	)
	return sObject(req("providers", sRecord(provider)))
}

// upstream: node_modules/typebox/build/system/settings/settings.mjs:maxErrors
const maxSchemaErrors = 8

type schemaError struct{ path, message string }

// formatSchemaPath is formatValidationPath: typebox does not escape "/" in an instance path, so a key containing one reads as nested.
func formatSchemaPath(instancePath []string, required string) string {
	segments := instancePath
	if required != "" {
		segments = append(slices.Clone(segments), required)
	}
	path := strings.ReplaceAll(strings.Join(segments, "/"), "/", ".")
	if path == "" {
		return "root"
	}
	return path
}

func (s *schema) check(node jsonNode, path []string, errs *[]schemaError) bool {
	fail := func(message string) bool {
		*errs = append(*errs, schemaError{formatSchemaPath(path, ""), message})
		return false
	}
	isNumber := node.kind == jsonNumber && !math.IsInf(node.number, 0) && !math.IsNaN(node.number)
	switch s.kind {
	case schemaUnknown:
		return true
	case schemaString:
		if node.kind != jsonString {
			return fail("must be string")
		}
		if s.minLength > 0 && len(utf16.Encode([]rune(node.text))) < s.minLength {
			return fail("must not have fewer than " + strconv.Itoa(s.minLength) + " characters")
		}
	case schemaNumber, schemaInteger:
		kind := "number"
		if s.kind == schemaInteger {
			kind = "integer"
		}
		if !isNumber || (s.kind == schemaInteger && node.number != math.Trunc(node.number)) {
			return fail("must be " + kind)
		}
		ok := true
		if s.minimum != nil && node.number < *s.minimum {
			ok = fail("must be >= " + strconv.FormatFloat(*s.minimum, 'f', -1, 64))
		}
		if s.maximum != nil && node.number > *s.maximum {
			ok = fail("must be <= " + strconv.FormatFloat(*s.maximum, 'f', -1, 64))
		}
		if s.exclusiveM != nil && node.number <= *s.exclusiveM {
			ok = fail("must be > " + strconv.FormatFloat(*s.exclusiveM, 'f', -1, 64))
		}
		return ok
	case schemaBoolean:
		if node.kind != jsonBool {
			return fail("must be boolean")
		}
	case schemaNull:
		if node.kind != jsonNull {
			return fail("must be null")
		}
	case schemaLiteral:
		if node.kind != jsonString || node.text != s.literal {
			return fail("must be equal to constant")
		}
	case schemaArray:
		if node.kind != jsonArray {
			return fail("must be array")
		}
		ok := true
		for i, item := range node.items {
			if !s.elem.check(item, append(slices.Clone(path), strconv.Itoa(i)), errs) {
				ok = false
			}
		}
		if s.maxItems > 0 && len(node.items) > s.maxItems {
			ok = fail("must not have more than " + strconv.Itoa(s.maxItems) + " items")
		}
		return ok
	case schemaObject:
		if node.kind != jsonObject {
			return fail("must be object")
		}
		ok := true
		var missing []string
		for _, prop := range s.props {
			if _, present := node.fields[prop.name]; !present && !prop.optional {
				missing = append(missing, prop.name)
			}
		}
		if len(missing) > 0 {
			*errs = append(*errs, schemaError{formatSchemaPath(path, missing[0]), "must have required properties " + strings.Join(missing, ", ")})
			ok = false
		}
		for _, prop := range s.props {
			if value, present := node.fields[prop.name]; present && !prop.schema.check(value, append(slices.Clone(path), prop.name), errs) {
				ok = false
			}
		}
		return ok
	case schemaRecord:
		if node.kind != jsonObject {
			return fail("must be object")
		}
		ok := true
		for _, key := range node.keys {
			if !s.elem.check(node.fields[key], append(slices.Clone(path), key), errs) {
				ok = false
			}
		}
		return ok
	case schemaUnion:
		var branchErrs []schemaError
		for _, alt := range s.alts {
			var altErrs []schemaError
			if alt.check(node, path, &altErrs) {
				return true
			}
			branchErrs = append(branchErrs, altErrs...)
		}
		*errs = append(*errs, branchErrs...)
		return fail("must match a schema in anyOf")
	}
	return true
}

// modelConfigSchemaErrors is validateModelsConfig.Errors(parsed) as ModelConfig.load lists them: "  - path: message" per error.
func modelConfigSchemaErrors(node jsonNode) []string {
	var errs []schemaError
	if modelConfigSchema.check(node, nil, &errs) {
		return nil
	}
	// typebox stops collecting after maxErrors (8) errors.
	errs = errs[:min(len(errs), maxSchemaErrors)]
	lines := make([]string, len(errs))
	for i, e := range errs {
		lines[i] = "  - " + e.path + ": " + e.message
	}
	return lines
}
