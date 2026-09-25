package llama

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Ports packages/coding-agent/src/extensions/llama/huggingface.ts.

const defaultHuggingFaceURL = "https://huggingface.co"

var (
	quantizationPattern = regexp.MustCompile(`(?i)(?:^|[-_.])((?:UD-)?(?:IQ\d(?:_[A-Z0-9]+)+|Q\d(?:_[A-Z0-9]+)+|BF16|F16|F32|MXFP\d(?:_[A-Z0-9]+)*))$`)
	shardSuffixPattern  = regexp.MustCompile(`-\d{5}-of-\d{5}$`)
	rateLimitPattern    = regexp.MustCompile(`(?:^|;)t=(\d+)`)
)

// localeCompare mirrors String.prototype.localeCompare under the root locale.
var localeCollator = collate.New(language.Und)

func localeCompare(left, right string) int { return localeCollator.CompareString(left, right) }

// HuggingFaceModel mirrors one search result.
type HuggingFaceModel struct {
	ID        string
	Downloads float64
}

// HuggingFaceQuantization mirrors one GGUF quantization. Size is nil when any
// file of the quantization has no reported size.
type HuggingFaceQuantization struct {
	Name string
	Size *float64
}

// HuggingFaceGated mirrors `false | "auto" | "manual"`; the empty value is
// false.
type HuggingFaceGated string

// HuggingFaceModelDetails mirrors the model details upstream reads.
type HuggingFaceModelDetails struct {
	ID            string
	Gated         HuggingFaceGated
	Quantizations []HuggingFaceQuantization
}

func payloadError(payload any, fallback string) string {
	if message, ok := stringField(payload, "error"); ok && message != "" {
		return message
	}
	return fallback
}

func parseRateLimitDelay(value string) float64 {
	match := rateLimitPattern.FindStringSubmatch(value)
	if match == nil {
		return 0
	}
	delay, _ := strconv.ParseFloat(match[1], 64)
	return delay
}

// jsNumber mirrors Number(value) for a header string; NaN reports 0 so the
// caller's `||` fallback applies.
func jsNumber(value string) float64 {
	trimmed := strings.TrimFunc(value, isJSWhitespace)
	if trimmed == "" {
		return 0
	}
	number, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(number) {
		return 0
	}
	return number
}

func readToken(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimFunc(string(data), isJSWhitespace)
}

// FindHuggingFaceToken mirrors findHuggingFaceToken; env reads one variable,
// with "" meaning unset.
func FindHuggingFaceToken(env func(string) string) string {
	if token := strings.TrimFunc(env("HF_TOKEN"), isJSWhitespace); token != "" {
		return token
	}
	var paths []string
	if path := env("HF_TOKEN_PATH"); path != "" {
		paths = append(paths, path)
	}
	if home := env("HF_HOME"); home != "" {
		paths = append(paths, filepath.Join(home, "token"))
	}
	if cache := env("XDG_CACHE_HOME"); cache != "" {
		paths = append(paths, filepath.Join(cache, "huggingface", "token"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".cache", "huggingface", "token"))
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if token := readToken(path); token != "" {
			return token
		}
	}
	return ""
}

// HuggingFaceClient mirrors huggingface.ts HuggingFaceClient.
type HuggingFaceClient struct {
	token   string
	baseURL string
}

// NewHuggingFaceClient mirrors the constructor; an empty baseURL selects
// https://huggingface.co.
func NewHuggingFaceClient(token, baseURL string) *HuggingFaceClient {
	if baseURL == "" {
		baseURL = defaultHuggingFaceURL
	}
	return &HuggingFaceClient{token: token, baseURL: strings.TrimRight(baseURL, "/")}
}

func (c *HuggingFaceClient) request(ctx context.Context, path string) (any, error) {
	headers := http.Header{}
	if c.token != "" {
		headers.Set("Authorization", "Bearer "+c.token)
	}
	response, err := fetchJSON(ctx, http.MethodGet, c.baseURL+path, headers, nil)
	if err != nil {
		return nil, err
	}
	if response.ok() {
		return response.payload, nil
	}
	fallback := "Hugging Face returned HTTP " + strconv.Itoa(response.status)
	if response.status == http.StatusTooManyRequests {
		delay := jsNumber(response.header.Get("Retry-After"))
		if delay == 0 {
			delay = parseRateLimitDelay(response.header.Get("Ratelimit"))
		}
		if delay != 0 {
			return nil, errors.New("Hugging Face rate limit reached; retry in " + jsNumberString(delay) + "s")
		}
		return nil, errors.New("Hugging Face rate limit reached")
	}
	return nil, errors.New(payloadError(response.payload, fallback))
}

// Search mirrors HuggingFaceClient.search.
func (c *HuggingFaceClient) Search(ctx context.Context, query string) ([]HuggingFaceModel, error) {
	params := formEncode(
		[2]string{"search", query},
		[2]string{"filter", "gguf"},
		[2]string{"sort", "downloads"},
		[2]string{"direction", "-1"},
		[2]string{"limit", "20"},
	)
	payload, err := c.request(ctx, "/api/models?"+params)
	if err != nil {
		return nil, err
	}
	entries, ok := payload.([]any)
	if !ok {
		return nil, errors.New("Hugging Face returned invalid search results")
	}
	models := []HuggingFaceModel{}
	for _, entry := range entries {
		id, ok := stringField(entry, "id")
		if !ok {
			continue
		}
		downloads, _ := numberField(entry, "downloads")
		models = append(models, HuggingFaceModel{ID: id, Downloads: downloads})
	}
	return models, nil
}

type quantizationSize struct {
	name     string
	total    float64
	complete bool
}

// Details mirrors HuggingFaceClient.details.
func (c *HuggingFaceClient) Details(ctx context.Context, id string) (HuggingFaceModelDetails, error) {
	segments := strings.Split(id, "/")
	for index, segment := range segments {
		segments[index] = percentEncode(segment, "-_.!~*'()", false)
	}
	payload, err := c.request(ctx, "/api/models/"+strings.Join(segments, "/")+"?blobs=true")
	if err != nil {
		return HuggingFaceModelDetails{}, err
	}
	if _, ok := payload.(map[string]any); !ok {
		return HuggingFaceModelDetails{}, errors.New("Hugging Face returned invalid model details")
	}
	details := HuggingFaceModelDetails{ID: id, Quantizations: sortQuantizations(collectQuantizations(payload))}
	if modelID, ok := stringField(payload, "id"); ok {
		details.ID = modelID
	}
	if gated, _ := stringField(payload, "gated"); gated == "auto" || gated == "manual" {
		details.Gated = HuggingFaceGated(gated)
	}
	return details, nil
}

// collectQuantizations groups GGUF files by quantization in first-seen order,
// mirroring the upstream Map.
func collectQuantizations(payload any) []quantizationSize {
	siblings, _ := objectField(payload, "siblings")
	files, _ := siblings.([]any)
	var sizes []quantizationSize
	for _, file := range files {
		name, ok := stringField(file, "rfilename")
		if !ok || !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		filename := name[strings.LastIndex(name, "/")+1:]
		if strings.HasPrefix(strings.ToLower(filename), "mmproj") {
			continue
		}
		stem := shardSuffixPattern.ReplaceAllString(filename[:len(filename)-5], "")
		match := quantizationPattern.FindStringSubmatch(stem)
		if match == nil || match[1] == "" {
			continue
		}
		quantization := strings.ToUpper(match[1])
		index := slices.IndexFunc(sizes, func(entry quantizationSize) bool { return entry.name == quantization })
		if index < 0 {
			sizes = append(sizes, quantizationSize{name: quantization, complete: true})
			index = len(sizes) - 1
		}
		if size, ok := numberField(file, "size"); ok {
			sizes[index].total += size
		} else {
			sizes[index].complete = false
		}
	}
	return sizes
}

func sortQuantizations(sizes []quantizationSize) []HuggingFaceQuantization {
	quantizations := make([]HuggingFaceQuantization, 0, len(sizes))
	for _, entry := range sizes {
		quantization := HuggingFaceQuantization{Name: entry.name}
		if entry.complete {
			quantization.Size = &entry.total
		}
		quantizations = append(quantizations, quantization)
	}
	sizeOrMax := func(size *float64) float64 {
		if size == nil {
			return 1<<53 - 1
		}
		return *size
	}
	slices.SortStableFunc(quantizations, func(left, right HuggingFaceQuantization) int {
		if left.Name == "Q4_K_M" {
			return -1
		}
		if right.Name == "Q4_K_M" {
			return 1
		}
		if bySize := cmp.Compare(sizeOrMax(left.Size), sizeOrMax(right.Size)); bySize != 0 {
			return bySize
		}
		return localeCompare(left.Name, right.Name)
	})
	return quantizations
}
