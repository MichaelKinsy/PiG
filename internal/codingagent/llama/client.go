// Package llama ports Pi's built-in llama.cpp extension
// (packages/coding-agent/src/extensions/llama/): the router HTTP/SSE client,
// Hugging Face search and download, the dynamic provider, the manager UI, and
// the /llama command.
package llama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Ports packages/coding-agent/src/extensions/llama/client.ts.

// LlamaModelStatus mirrors the llama.cpp router model status union.
type LlamaModelStatus string

const (
	LlamaModelStatusUnloaded    LlamaModelStatus = "unloaded"
	LlamaModelStatusLoading     LlamaModelStatus = "loading"
	LlamaModelStatusLoaded      LlamaModelStatus = "loaded"
	LlamaModelStatusDownloading LlamaModelStatus = "downloading"
	LlamaModelStatusSleeping    LlamaModelStatus = "sleeping"
)

// LlamaModelInfoStatus mirrors LlamaModelInfo.status. Progress keeps the raw
// decoded JSON object because upstream only sums its entries.
type LlamaModelInfoStatus struct {
	Value    LlamaModelStatus
	Args     []string
	Failed   bool
	ExitCode *float64
	Progress any
}

// LlamaModelArchitecture mirrors LlamaModelInfo.architecture.
type LlamaModelArchitecture struct {
	InputModalities  []string
	OutputModalities []string
}

// LlamaModelMeta mirrors LlamaModelInfo.meta. Pointers keep an absent value
// distinct from zero for the `??` reads upstream performs.
type LlamaModelMeta struct {
	NCtx      *float64
	NCtxTrain *float64
	Size      *float64
	Ftype     string
}

// LlamaModelInfo mirrors one entry of the router's /models catalog.
type LlamaModelInfo struct {
	ID           string
	Aliases      []string
	Status       LlamaModelInfoStatus
	Architecture *LlamaModelArchitecture
	Source       string
	Meta         *LlamaModelMeta
}

// LlamaServerProps mirrors the /props fields upstream reads.
type LlamaServerProps struct {
	ModelsAutoload bool
	ChatTemplate   string
}

// LlamaModelEvent mirrors one /models/sse event.
type LlamaModelEvent struct {
	Model string
	Event string
	Data  any
}

// LlamaProgress mirrors a load or download progress update. Ratio is nil when
// upstream leaves it undefined.
type LlamaProgress struct {
	Message string
	Ratio   *float64
	Detail  string
	// keys records which optional properties the update object carries, so
	// Object.assign in runWithProgress replaces only those.
	keys progressKeys
}

type progressKeys uint8

const (
	progressRatioKey progressKeys = 1 << iota
	progressDetailKey
)

func errorMessage(payload any, fallback string) string {
	errorValue, _ := objectField(payload, "error")
	if message, ok := stringField(errorValue, "message"); ok && message != "" {
		return message
	}
	return fallback
}

// decodeModelInfo mirrors isModelInfo and keeps the optional fields whose
// JSON types match what upstream reads.
func decodeModelInfo(value any) (LlamaModelInfo, bool) {
	id, ok := stringField(value, "id")
	if !ok {
		return LlamaModelInfo{}, false
	}
	status, _ := objectField(value, "status")
	statusValue, ok := stringField(status, "value")
	if !ok {
		return LlamaModelInfo{}, false
	}
	info := LlamaModelInfo{ID: id, Status: LlamaModelInfoStatus{Value: LlamaModelStatus(statusValue)}}
	info.Aliases = stringList(value, "aliases")
	info.Status.Args = stringList(status, "args")
	failed, _ := objectField(status, "failed")
	info.Status.Failed = jsTruthy(failed)
	if code, ok := numberField(status, "exit_code"); ok {
		info.Status.ExitCode = &code
	}
	info.Status.Progress, _ = objectField(status, "progress")
	if architecture, ok := objectField(value, "architecture"); ok {
		if _, isObject := architecture.(map[string]any); isObject {
			info.Architecture = &LlamaModelArchitecture{
				InputModalities:  stringList(architecture, "input_modalities"),
				OutputModalities: stringList(architecture, "output_modalities"),
			}
		}
	}
	info.Source, _ = stringField(value, "source")
	if meta, ok := objectField(value, "meta"); ok {
		if _, isObject := meta.(map[string]any); isObject {
			info.Meta = &LlamaModelMeta{
				NCtx:      optionalNumber(meta, "n_ctx"),
				NCtxTrain: optionalNumber(meta, "n_ctx_train"),
				Size:      optionalNumber(meta, "size"),
			}
			info.Meta.Ftype, _ = stringField(meta, "ftype")
		}
	}
	return info, true
}

func stringList(value any, key string) []string {
	field, _ := objectField(value, key)
	items, ok := field.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func optionalNumber(value any, key string) *float64 {
	if number, ok := numberField(value, key); ok {
		return &number
	}
	return nil
}

func parseLoadProgress(data any) (LlamaProgress, bool) {
	progress, _ := objectField(data, "progress")
	if _, ok := progress.(map[string]any); !ok {
		return LlamaProgress{}, false
	}
	stage, ok := stringField(progress, "current")
	if !ok {
		stage, _ = stringField(progress, "stage")
	}
	stages := stringList(progress, "stages")
	var ratio *float64
	if value, ok := numberField(progress, "value"); ok {
		clamped := max(0, min(1, value))
		ratio = &clamped
	}
	if stage != "" && len(stages) > 0 {
		if index := slices.Index(stages, stage); index >= 0 {
			stageRatio := 0.0
			if ratio != nil {
				stageRatio = *ratio
			}
			overall := (float64(index) + stageRatio) / float64(len(stages))
			ratio = &overall
		}
	}
	message := "Loading model"
	if stage != "" {
		message = "Loading " + strings.ReplaceAll(stage, "_", " ")
	}
	return LlamaProgress{Message: message, Ratio: ratio, keys: progressRatioKey}, true
}

func parseDownloadProgress(data any) (LlamaProgress, bool) {
	files, ok := objectValues(data)
	if !ok {
		return LlamaProgress{}, false
	}
	nested, _ := objectField(data, "progress")
	if values, ok := objectValues(nested); ok {
		files = values
	}
	done, total := 0.0, 0.0
	for _, value := range files {
		entryDone, doneOK := numberField(value, "done")
		entryTotal, totalOK := numberField(value, "total")
		if !doneOK || !totalOK {
			continue
		}
		done += entryDone
		total += entryTotal
	}
	if total <= 0 {
		return LlamaProgress{}, false
	}
	ratio := done / total
	return LlamaProgress{
		Message: "Downloading model",
		Ratio:   &ratio,
		Detail:  FormatBytes(done) + " / " + FormatBytes(total),
		keys:    progressRatioKey | progressDetailKey,
	}, true
}

// FormatBytes mirrors client.ts formatBytes.
func FormatBytes(bytes float64) string {
	if bytes < 1024 {
		return jsNumberString(bytes) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	value := bytes / 1024
	unit := units[0]
	for index := 1; index < len(units) && value >= 1024; index++ {
		value /= 1024
		unit = units[index]
	}
	if value >= 10 {
		return jsToFixed(value, 1) + " " + unit
	}
	return jsToFixed(value, 2) + " " + unit
}

// jsNumberString mirrors template-literal number formatting for the byte
// counts FormatBytes prints below 1 KiB.
func jsNumberString(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// NormalizeLlamaServerURL mirrors client.ts normalizeLlamaServerUrl.
func NormalizeLlamaServerURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimFunc(value, isJSWhitespace))
	if err != nil || parsed.Scheme == "" {
		return "", errors.New("Invalid URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("Server URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("Invalid URL")
	}
	host := strings.ToLower(parsed.Host)
	if port := parsed.Port(); (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	path = strings.TrimSuffix(path, "/v1")
	userinfo := ""
	if parsed.User != nil {
		userinfo = parsed.User.String() + "@"
	}
	return strings.TrimSuffix(scheme+"://"+userinfo+host+path, "/"), nil
}

// LlamaInferenceURL mirrors client.ts llamaInferenceUrl.
func LlamaInferenceURL(serverURL string) (string, error) {
	normalized, err := NormalizeLlamaServerURL(serverURL)
	if err != nil {
		return "", err
	}
	return normalized + "/v1", nil
}

// LlamaClient mirrors client.ts LlamaClient.
type LlamaClient struct {
	ServerURL string
	apiKey    string
}

// NewLlamaClient mirrors the LlamaClient constructor, which normalizes the
// server URL and may throw.
func NewLlamaClient(serverURL, apiKey string) (*LlamaClient, error) {
	normalized, err := NormalizeLlamaServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	return &LlamaClient{ServerURL: normalized, apiKey: apiKey}, nil
}

func (c *LlamaClient) headers() http.Header {
	headers := http.Header{}
	if c.apiKey != "" {
		headers.Set("Authorization", "Bearer "+c.apiKey)
	}
	return headers
}

func (c *LlamaClient) request(ctx context.Context, method, path string, body any) (any, error) {
	headers := c.headers()
	var encoded []byte
	if body != nil {
		var err error
		if encoded, err = json.Marshal(body); err != nil {
			return nil, err
		}
		headers.Set("Content-Type", "application/json")
	}
	response, err := fetchJSON(ctx, method, c.ServerURL+path, headers, encoded)
	if err != nil {
		return nil, err
	}
	if !response.ok() {
		return nil, errors.New(errorMessage(response.payload, fmt.Sprintf("llama.cpp returned HTTP %d", response.status)))
	}
	return response.payload, nil
}

// List mirrors LlamaClient.list.
func (c *LlamaClient) List(ctx context.Context, reload bool) ([]LlamaModelInfo, error) {
	path := "/models"
	if reload {
		path += "?reload=1"
	}
	payload, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	data, _ := objectField(payload, "data")
	entries, ok := data.([]any)
	if !ok {
		return nil, errors.New("llama.cpp returned an invalid model catalog")
	}
	models := make([]LlamaModelInfo, 0, len(entries))
	for _, entry := range entries {
		model, ok := decodeModelInfo(entry)
		if !ok {
			return nil, errors.New("Server is not running in llama.cpp router mode")
		}
		models = append(models, model)
	}
	return models, nil
}

// Props mirrors LlamaClient.props.
func (c *LlamaClient) Props(ctx context.Context, model string) (LlamaServerProps, error) {
	query := ""
	if model != "" {
		query = "?" + formEncode([2]string{"model", model}, [2]string{"autoload", "false"})
	}
	payload, err := c.request(ctx, http.MethodGet, "/props"+query, nil)
	if err != nil {
		return LlamaServerProps{}, err
	}
	var props LlamaServerProps
	if autoload, ok := objectField(payload, "models_autoload"); ok {
		props.ModelsAutoload = autoload == true
	}
	props.ChatTemplate, _ = stringField(payload, "chat_template")
	return props, nil
}

// Load mirrors LlamaClient.load.
func (c *LlamaClient) Load(ctx context.Context, model string) error {
	_, err := c.request(ctx, http.MethodPost, "/models/load", map[string]string{"model": model})
	return err
}

// Unload mirrors LlamaClient.unload.
func (c *LlamaClient) Unload(ctx context.Context, model string) error {
	_, err := c.request(ctx, http.MethodPost, "/models/unload", map[string]string{"model": model})
	return err
}

// UnloadAndWait mirrors LlamaClient.unloadAndWait.
func (c *LlamaClient) UnloadAndWait(ctx context.Context, model string) error {
	if err := c.Unload(ctx, model); err != nil {
		return err
	}
	for {
		models, err := c.List(ctx, false)
		if err != nil {
			return err
		}
		entry := findModel(models, model)
		if entry == nil || entry.Status.Value == LlamaModelStatusUnloaded {
			return nil
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

// Download mirrors LlamaClient.download.
func (c *LlamaClient) Download(ctx context.Context, model string) error {
	_, err := c.request(ctx, http.MethodPost, "/models", map[string]string{"model": model})
	return err
}

func findModel(models []LlamaModelInfo, id string) *LlamaModelInfo {
	for index := range models {
		if models[index].ID == id {
			return &models[index]
		}
	}
	return nil
}

// Watch mirrors LlamaClient.watch: it reads /models/sse until the stream ends
// or ctx is cancelled.
func (c *LlamaClient) Watch(ctx context.Context, onEvent func(LlamaModelEvent)) error {
	return c.watch(ctx, onEvent, nil)
}

func (c *LlamaClient) watch(ctx context.Context, onEvent func(LlamaModelEvent), onSent func()) error {
	response, err := openStream(ctx, c.ServerURL+"/models/sse", c.headers(), onSent)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("llama.cpp SSE returned HTTP %d", response.StatusCode)
	}
	var decoder utf8StreamDecoder
	buffer := ""
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := response.Body.Read(chunk)
		if n > 0 {
			buffer += strings.ReplaceAll(decoder.decode(chunk[:n]), "\r\n", "\n")
			buffer = dispatchFrames(buffer, onEvent)
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return abortReason(ctx)
			}
			return readErr
		}
	}
}

// dispatchFrames emits every complete SSE frame in buffer and returns the
// unconsumed remainder.
func dispatchFrames(buffer string, onEvent func(LlamaModelEvent)) string {
	for {
		boundary := strings.Index(buffer, "\n\n")
		if boundary < 0 {
			return buffer
		}
		frame := buffer[:boundary]
		buffer = buffer[boundary+2:]
		var data []string
		// upstream: coding-agent/src/extensions/llama/client.ts:LlamaModelEvent
		for line := range strings.SplitSeq(frame, "\n") {
			if rest, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimLeftFunc(rest, isJSWhitespace))
			}
		}
		joined := strings.Join(data, "\n")
		if joined == "" {
			continue
		}
		// Ignore malformed events; catalog polling remains authoritative.
		var event any
		// upstream: coding-agent/src/extensions/llama/client.ts:LlamaModelEvent
		if json.Unmarshal([]byte(joined), &event) != nil {
			continue
		}
		model, modelOK := stringField(event, "model")
		name, nameOK := stringField(event, "event")
		if modelOK && nameOK {
			payload, _ := objectField(event, "data")
			onEvent(LlamaModelEvent{Model: model, Event: name, Data: payload})
		}
	}
}

// utf8StreamDecoder mirrors TextDecoder's streaming mode: a multi-byte
// sequence split across chunks is held until complete, and a leading BOM is
// dropped.
type utf8StreamDecoder struct {
	pending []byte
	started bool
}

func (d *utf8StreamDecoder) decode(chunk []byte) string {
	data := slices.Concat(d.pending, chunk)
	cut := len(data)
	for index := len(data) - 1; index >= 0 && index >= len(data)-utf8.UTFMax; index-- {
		if utf8.RuneStart(data[index]) {
			if !utf8.FullRune(data[index:]) {
				cut = index
			}
			break
		}
	}
	d.pending = append([]byte(nil), data[cut:]...)
	text := string(data[:cut])
	if !d.started && cut > 0 {
		d.started = true
		text = strings.TrimPrefix(text, "\uFEFF")
	}
	return text
}

// progressSink serializes progress callbacks and event state shared by the
// watcher goroutine and the polling loop.
type progressSink struct {
	mu         sync.Mutex
	onProgress func(LlamaProgress)
}

func (s *progressSink) report(progress LlamaProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onProgress(progress)
}

// startWatcher mirrors `void this.watch(...).catch(() => {})` with a linked
// abort controller. It returns once the SSE request is written, preserving
// upstream's order of watch before load, and a stop function that aborts the
// watcher and waits for it to exit.
func (c *LlamaClient) startWatcher(ctx context.Context, onEvent func(LlamaModelEvent)) func() {
	watchCtx, cancel := context.WithCancel(ctx)
	sent := make(chan struct{})
	var group sync.WaitGroup
	group.Go(func() {
		_ = c.watch(watchCtx, onEvent, func() { close(sent) })
	})
	<-sent
	return func() {
		cancel()
		group.Wait()
	}
}

// LoadAndWait mirrors LlamaClient.loadAndWait.
func (c *LlamaClient) LoadAndWait(ctx context.Context, model string, onProgress func(LlamaProgress)) (LlamaModelInfo, error) {
	sink := &progressSink{onProgress: onProgress}
	eventLoaded := false
	eventError := ""
	stop := c.startWatcher(ctx, func(event LlamaModelEvent) {
		if event.Model != model || (event.Event != "model_status" && event.Event != "status_change") {
			return
		}
		sink.mu.Lock()
		defer sink.mu.Unlock()
		status, _ := objectField(event.Data, "status")
		if status == "loaded" {
			eventLoaded = true
		}
		if status == "unloaded" {
			eventError = "Model failed to load"
		}
		if progress, ok := parseLoadProgress(event.Data); ok {
			sink.onProgress(progress)
		}
	})
	defer stop()
	if err := c.Load(ctx, model); err != nil {
		return LlamaModelInfo{}, err
	}
	sink.report(LlamaProgress{Message: "Loading model"})
	for {
		if ctx.Err() != nil {
			return LlamaModelInfo{}, abortReason(ctx)
		}
		models, err := c.List(ctx, false)
		if err != nil {
			return LlamaModelInfo{}, err
		}
		entry := findModel(models, model)
		if entry != nil && entry.Status.Value == LlamaModelStatusLoaded {
			return *entry, nil
		}
		sink.mu.Lock()
		loaded, failure := eventLoaded, eventError
		sink.mu.Unlock()
		if loaded && entry == nil {
			return LlamaModelInfo{ID: model, Status: LlamaModelInfoStatus{Value: LlamaModelStatusLoaded}}, nil
		}
		if (entry != nil && entry.Status.Failed) || failure != "" {
			return LlamaModelInfo{}, loadFailure(entry, failure)
		}
		if err := sleep(ctx, 250*time.Millisecond); err != nil {
			return LlamaModelInfo{}, err
		}
	}
}

func loadFailure(entry *LlamaModelInfo, eventError string) error {
	if entry == nil || entry.Status.ExitCode == nil {
		if eventError != "" {
			return errors.New(eventError)
		}
		return errors.New("Model failed to load")
	}
	return fmt.Errorf("Model exited with code %s", jsNumberString(*entry.Status.ExitCode))
}

// DownloadAndWait mirrors LlamaClient.downloadAndWait.
func (c *LlamaClient) DownloadAndWait(ctx context.Context, model string, onProgress func(LlamaProgress)) ([]LlamaModelInfo, error) {
	sink := &progressSink{onProgress: onProgress}
	finished := false
	failure := ""
	sawDownloading := false
	stop := c.startWatcher(ctx, func(event LlamaModelEvent) {
		if event.Model != model {
			return
		}
		sink.mu.Lock()
		defer sink.mu.Unlock()
		switch event.Event {
		case "download_finished":
			finished = true
		case "download_failed":
			failure = errorMessage(event.Data, "Download failed")
		case "download_progress":
			sawDownloading = true
			if progress, ok := parseDownloadProgress(event.Data); ok {
				sink.onProgress(progress)
			}
		}
	})
	defer stop()
	if err := c.Download(ctx, model); err != nil {
		return nil, err
	}
	sink.report(LlamaProgress{Message: "Downloading model"})
	for polls := 0; ; {
		if ctx.Err() != nil {
			return nil, abortReason(ctx)
		}
		sink.mu.Lock()
		failed := failure
		sink.mu.Unlock()
		if failed != "" {
			return nil, errors.New(failed)
		}
		models, err := c.List(ctx, false)
		if err != nil {
			return nil, err
		}
		polls++
		entry := findModel(models, model)
		sink.mu.Lock()
		done := false
		if entry != nil && entry.Status.Value == LlamaModelStatusDownloading {
			sawDownloading = true
			if progress, ok := parseDownloadProgress(entry.Status.Progress); ok {
				sink.onProgress(progress)
			}
		} else {
			done = finished || (entry != nil && (sawDownloading || polls >= 2))
		}
		sink.mu.Unlock()
		if done {
			return c.List(ctx, true)
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
	}
}
