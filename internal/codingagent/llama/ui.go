package llama

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/src/extensions/llama/ui.ts.

// LlamaManagerActionType mirrors the LlamaManagerAction union tags.
type LlamaManagerActionType string

const (
	LlamaManagerActionModel    LlamaManagerActionType = "model"
	LlamaManagerActionDownload LlamaManagerActionType = "download"
	LlamaManagerActionClose    LlamaManagerActionType = "close"
)

// LlamaManagerAction mirrors LlamaManagerAction; Model is set for "model".
type LlamaManagerAction struct {
	Type  LlamaManagerActionType
	Model LlamaModelInfo
}

// ProgressState mirrors ui.ts ProgressState.
type ProgressState struct {
	LlamaProgress
	Title string
	Model string
}

// assign mirrors Object.assign(state, progress): message always, ratio and
// detail only when the update carries those keys.
func (s *ProgressState) assign(progress LlamaProgress) {
	s.Message = progress.Message
	if progress.keys&progressRatioKey != 0 {
		s.Ratio = progress.Ratio
	}
	if progress.keys&progressDetailKey != 0 {
		s.Detail = progress.Detail
	}
}

// HuggingFaceSearchFunc performs one Hugging Face query.
type HuggingFaceSearchFunc func(ctx context.Context, query string) ([]HuggingFaceModel, error)

// LlamaUi mirrors ui.ts LlamaUi. Blocking methods return when the user
// answers; Progress returns a channel closed when the user asks to stop.
type LlamaUi interface {
	ShowModels(serverURL string, models []LlamaModelInfo) LlamaManagerAction
	Select(title string, options []string) (string, bool)
	Confirm(title, message string) bool
	ConnectionError(serverURL, message string) string
	SearchModels(search HuggingFaceSearchFunc) (string, bool)
	ShowStatus(title, message string)
	Progress(state ProgressState) <-chan struct{}
	UpdateProgress(state ProgressState)
}

// CustomComponent is the editor-slot component a command shows, mirroring
// the component ctx.ui.custom factories return.
type CustomComponent interface {
	tui.Component
	HandleInput(data string)
}

func contextLabel(model LlamaModelInfo) string {
	var context *float64
	if model.Meta != nil {
		context = model.Meta.NCtx
		if context == nil {
			context = model.Meta.NCtxTrain
		}
	}
	if context != nil && *context != 0 {
		return formatContext(*context)
	}
	args := model.Status.Args
	for index := 0; index < len(args)-1; index++ {
		if args[index] != "--ctx-size" && args[index] != "-c" && args[index] != "-ctx" {
			continue
		}
		value, ok := jsNumberValue(args[index+1])
		if ok && !math.IsInf(value, 0) && value > 0 {
			return formatContext(value)
		}
	}
	return ""
}

func formatContext(value float64) string {
	if value >= 1000 {
		return jsNumberString(jsRound(value/1000)) + "k"
	}
	return jsNumberString(value)
}

// jsNumberValue mirrors Number(text); ok is false for NaN.
func jsNumberValue(text string) (float64, bool) {
	trimmed := strings.TrimFunc(text, isJSWhitespace)
	if trimmed == "" {
		return 0, true
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	return value, err == nil
}

// jsRound mirrors Math.round, which rounds halves toward +Infinity.
func jsRound(value float64) float64 { return math.Floor(value + 0.5) }

func modelDescription(model LlamaModelInfo) string {
	var details []string
	loaded := model.Status.Value == LlamaModelStatusLoaded || model.Status.Value == LlamaModelStatusSleeping
	if loaded {
		details = append(details, "loaded")
	} else if model.Status.Value != LlamaModelStatusUnloaded {
		details = append(details, string(model.Status.Value))
	}
	if loaded {
		if context := contextLabel(model); context != "" {
			details = append(details, context+" context")
		}
	}
	return strings.Join(details, " · ")
}

func fg(token, text string) string { return tui.ActiveTheme().FgText(token, text) }

// keyHint mirrors keybinding-hints.ts keyHint.
func keyHint(action, description string) string {
	keys := tui.GetTUIKeybindings().GetKeys(action)
	return fg("dim", tui.FormatKeyText(strings.Join(keys, "/"), false)) + fg("muted", " "+description)
}

func paddedText(content string) *tui.Text { return tui.NewPaddedText(content, 1, 0, nil) }

func frame(title string, body []tui.Component, footer string) *tui.Container {
	container := tui.NewContainer()
	container.Add(tui.NewDynamicBorder(tui.ActiveTheme().Fg("accent")))
	container.Add(paddedText(fg("accent", "\x1b[1m"+title+tui.SGRBoldDimReset)))
	for _, child := range body {
		container.Add(child)
	}
	if footer != "" {
		container.Add(tui.NewSpacer(1))
		container.Add(paddedText(fg("dim", footer)))
	}
	container.Add(tui.NewDynamicBorder(tui.ActiveTheme().Fg("accent")))
	return container
}

func compactCount(value float64) string {
	if value >= 1_000_000 {
		digits := 1
		if value >= 10_000_000 {
			digits = 0
		}
		return jsToFixed(value/1_000_000, digits) + "M"
	}
	if value >= 1_000 {
		digits := 1
		if value >= 100_000 {
			digits = 0
		}
		return jsToFixed(value/1_000, digits) + "k"
	}
	return jsNumberString(value)
}

// newSelectList mirrors `new SelectList(items, maxVisible, theme, layout)`
// over pig's SelectList port.
func newSelectList(labels, descriptions []string, minPrimary, maxPrimary int) *tui.FilterableList {
	list := tui.NewFilterableList("", labels)
	list.EnableSearch = false
	list.Descriptions = descriptions
	list.MinPrimaryColumnWidth = minPrimary
	list.MaxPrimaryColumnWidth = maxPrimary
	list.MaxVisible = min(len(labels), 12)
	return list
}

// jsWhitespaceClass mirrors the characters JavaScript's \s matches.
const jsWhitespaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var exactModelPattern = regexp.MustCompile(`^[^/` + jsWhitespaceClass + `]+/[^:` + jsWhitespaceClass + `]+(?::[^` + jsWhitespaceClass + `:]+)?$`)

// jsLength mirrors String.prototype.length in UTF-16 code units.
func jsLength(text string) int { return len(utf16.Encode([]rune(text))) }

const searchingStatus = "Searching Hugging Face…"

// HuggingFaceSearch mirrors ui.ts HuggingFaceSearch. Its state is guarded by
// the owning view's lock; the debounce timer and search goroutine take it.
type HuggingFaceSearch struct {
	lock          *sync.Mutex
	requestRender func()
	search        HuggingFaceSearchFunc
	cache         map[string][]HuggingFaceModel
	onSelectModel func(model string, ok bool)

	input           *tui.TextInput
	container       *tui.Container
	results         *tui.Container
	allResults      []HuggingFaceModel
	filteredResults []HuggingFaceModel
	selectedIndex   int
	query           string
	status          string
	debounce        *time.Timer
	cancelRequest   context.CancelFunc
	request         int
	closed          bool
	tasks           sync.WaitGroup
}

func newHuggingFaceSearch(lock *sync.Mutex, requestRender func(), search HuggingFaceSearchFunc, cache map[string][]HuggingFaceModel, onSelectModel func(string, bool)) *HuggingFaceSearch {
	component := &HuggingFaceSearch{
		lock:          lock,
		requestRender: requestRender,
		search:        search,
		cache:         cache,
		onSelectModel: onSelectModel,
		input:         tui.NewTextInput(""),
		results:       tui.NewContainer(),
		status:        "Type at least 2 characters",
	}
	component.container = tui.NewContainer(
		paddedText(fg("dim", "Model name or owner/repository[:quant]")),
		component.input,
		tui.NewSpacer(1),
		component.results,
	)
	component.updateResults()
	return component
}

// Render draws the prompt, input, and results. The component reports no
// dirty state, so its container renders it every frame.
func (s *HuggingFaceSearch) Render(width int) []string { return s.container.Render(width) }

// Invalidate invalidates the prompt, input, and results.
func (s *HuggingFaceSearch) Invalidate() { s.container.Invalidate() }

func (s *HuggingFaceSearch) updateResults() {
	s.results.Clear()
	const maxVisible = 10
	start := max(0, min(s.selectedIndex-maxVisible/2, len(s.filteredResults)-maxVisible))
	end := min(start+maxVisible, len(s.filteredResults))
	for index := start; index < end; index++ {
		model := s.filteredResults[index]
		details := compactCount(model.Downloads) + " downloads"
		if index == s.selectedIndex {
			s.results.Add(tui.NewPaddedText(fg("accent", "→ "+model.ID+"  "+details), 0, 0, nil))
		} else {
			s.results.Add(tui.NewPaddedText("  "+model.ID+fg("muted", "  "+details), 0, 0, nil))
		}
	}
	if start > 0 || end < len(s.filteredResults) {
		s.results.Add(tui.NewPaddedText(fg("dim", "  ("+strconv.Itoa(s.selectedIndex+1)+"/"+strconv.Itoa(len(s.filteredResults))+")"), 0, 0, nil))
	}
	if len(s.filteredResults) == 0 || s.status == searchingStatus {
		s.results.Add(tui.NewPaddedText(fg("dim", "  "+s.status), 0, 0, nil))
	}
	s.container.Invalidate()
	s.requestRender()
}

func (s *HuggingFaceSearch) filterResults() {
	if s.query != "" {
		matches := map[string]bool{}
		for _, model := range tui.FuzzyFilter(s.allResults, s.query, func(model HuggingFaceModel) string { return model.ID }) {
			matches[model.ID] = true
		}
		s.filteredResults = nil
		for _, model := range s.allResults {
			if matches[model.ID] {
				s.filteredResults = append(s.filteredResults, model)
			}
		}
	} else {
		s.filteredResults = s.allResults
	}
	s.selectedIndex = min(s.selectedIndex, max(0, len(s.filteredResults)-1))
	s.updateResults()
}

func (s *HuggingFaceSearch) stopPending() {
	if s.debounce != nil && s.debounce.Stop() {
		s.tasks.Done()
	}
	s.debounce = nil
	if s.cancelRequest != nil {
		s.cancelRequest()
		s.cancelRequest = nil
	}
}

func (s *HuggingFaceSearch) scheduleSearch() {
	s.stopPending()
	if jsLength(s.query) < 2 {
		s.status = "Type at least 2 characters"
		s.filterResults()
		return
	}
	if cached, ok := s.cache[strings.ToLower(s.query)]; ok {
		s.allResults = cached
		s.status = ""
		if len(cached) == 0 {
			s.status = "No GGUF models found"
		}
		s.filterResults()
		return
	}
	s.status = searchingStatus
	s.filterResults()
	query := s.query
	s.tasks.Add(1)
	s.debounce = time.AfterFunc(500*time.Millisecond, func() {
		defer s.tasks.Done()
		s.runSearch(query)
	})
}

func (s *HuggingFaceSearch) runSearch(query string) {
	s.lock.Lock()
	if s.closed {
		s.lock.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.request++
	request := s.request
	s.cancelRequest = cancel
	s.lock.Unlock()
	defer cancel()

	results, err := s.search(ctx, query)
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.request == request {
		s.cancelRequest = nil
	}
	if err == nil {
		s.cache[strings.ToLower(query)] = results
	}
	if s.closed || ctx.Err() != nil || s.query != query {
		return
	}
	if err != nil {
		s.allResults = nil
		s.status = err.Error()
		s.filterResults()
		return
	}
	s.allResults = results
	s.selectedIndex = 0
	s.status = ""
	if len(results) == 0 {
		s.status = "No GGUF models found"
	}
	s.filterResults()
}

func (s *HuggingFaceSearch) close(model string, ok bool) {
	if s.closed {
		return
	}
	s.closed = true
	s.stopPending()
	s.onSelectModel(model, ok)
}

// HandleInput mirrors HuggingFaceSearch.handleInput; the caller holds the
// view lock.
func (s *HuggingFaceSearch) HandleInput(data string) {
	keys := tui.GetTUIKeybindings()
	switch {
	case keys.Matches(data, tui.KBSelectUp):
		if len(s.filteredResults) > 0 {
			s.selectedIndex = (s.selectedIndex - 1 + len(s.filteredResults)) % len(s.filteredResults)
			s.updateResults()
		}
	case keys.Matches(data, tui.KBSelectDown):
		if len(s.filteredResults) > 0 {
			s.selectedIndex = (s.selectedIndex + 1) % len(s.filteredResults)
			s.updateResults()
		}
	case keys.Matches(data, tui.KBSelectConfirm):
		selected := ""
		if exactModelPattern.MatchString(s.query) {
			selected = s.query
		} else if s.selectedIndex < len(s.filteredResults) {
			selected = s.filteredResults[s.selectedIndex].ID
		}
		if selected != "" {
			s.close(selected, true)
		}
	case keys.Matches(data, tui.KBSelectCancel):
		s.close("", false)
	default:
		s.input.HandleInput(data)
		query := strings.TrimFunc(s.input.Text(), isJSWhitespace)
		if query == s.query {
			return
		}
		s.query = query
		s.scheduleSearch()
	}
}

// LlamaView mirrors ui.ts LlamaView. The command's flow goroutine calls the
// LlamaUi methods while the interactive loop calls Render and HandleInput;
// one lock orders both, as the single JavaScript event loop does upstream.
type LlamaView struct {
	mu              sync.Mutex
	requestRender   func()
	searchCache     map[string][]HuggingFaceModel
	content         tui.Component
	inputHandler    func(data string)
	progressStop    chan struct{}
	showingProgress bool
}

// NewLlamaView creates the manager view; requestRender asks the host to
// repaint after the flow changes the view.
func NewLlamaView(requestRender func()) *LlamaView {
	return &LlamaView{
		requestRender: requestRender,
		searchCache:   map[string][]HuggingFaceModel{},
		content:       frame("llama.cpp models", []tui.Component{tui.NewPaddedText(fg("muted", "Loading…"), 1, 1, nil)}, ""),
	}
}

func (v *LlamaView) setContentLocked(content tui.Component, inputHandler func(string)) {
	v.progressStop = nil
	v.showingProgress = false
	v.content = content
	v.inputHandler = inputHandler
}

func (v *LlamaView) setContent(content tui.Component, inputHandler func(string)) {
	v.mu.Lock()
	v.setContentLocked(content, inputHandler)
	v.mu.Unlock()
	v.requestRender()
}

// showList shows list in a frame and blocks until it is confirmed or
// cancelled, returning the selected index or -1.
func (v *LlamaView) showList(list *tui.FilterableList, title string, before []tui.Component, footer string) int {
	result := make(chan int, 1)
	body := append(slices.Clone(before), list)
	v.setContent(frame(title, body, footer), func(data string) {
		list.HandleInput(data)
		if list.Done() {
			select {
			case result <- list.SelectedIndex(): // upstream: coding-agent/src/extensions/llama/ui.ts:onSelect
			default:
			}
		}
	})
	return <-result
}

// ShowModels mirrors LlamaView.showModels.
func (v *LlamaView) ShowModels(serverURL string, models []LlamaModelInfo) LlamaManagerAction {
	sorted := slices.Clone(models)
	slices.SortStableFunc(sorted, func(left, right LlamaModelInfo) int {
		leftLoaded := left.Status.Value == LlamaModelStatusLoaded
		rightLoaded := right.Status.Value == LlamaModelStatusLoaded
		if leftLoaded != rightLoaded {
			if leftLoaded {
				return -1
			}
			return 1
		}
		return localeCompare(left.ID, right.ID)
	})
	labels := make([]string, 0, len(sorted)+1)
	descriptions := make([]string, 0, len(sorted)+1)
	for _, model := range sorted {
		labels = append(labels, model.ID)
		descriptions = append(descriptions, modelDescription(model))
	}
	labels = append(labels, "Download model…")
	descriptions = append(descriptions, "Hugging Face owner/repository[:quant]")
	list := newSelectList(labels, descriptions, 36, 56)
	index := v.showList(list, "llama.cpp models",
		[]tui.Component{paddedText(fg("dim", serverURL)), tui.NewSpacer(1)},
		keyHint(tui.KBSelectConfirm, "load/unload/download")+" • "+keyHint(tui.KBSelectCancel, "close"))
	switch {
	case index < 0:
		return LlamaManagerAction{Type: LlamaManagerActionClose}
	case index == len(sorted):
		return LlamaManagerAction{Type: LlamaManagerActionDownload}
	}
	return LlamaManagerAction{Type: LlamaManagerActionModel, Model: sorted[index]}
}

// Select mirrors LlamaView.select.
func (v *LlamaView) Select(title string, options []string) (string, bool) {
	list := newSelectList(options, nil, 0, 0)
	index := v.showList(list, title, []tui.Component{tui.NewSpacer(1)},
		keyHint(tui.KBSelectConfirm, "select")+" • "+keyHint(tui.KBSelectCancel, "cancel"))
	if index < 0 {
		return "", false
	}
	return options[index], true
}

// Confirm mirrors LlamaView.confirm.
func (v *LlamaView) Confirm(title, message string) bool {
	choice, ok := v.Select(title+"\n"+message, []string{"Yes", "No"})
	return ok && choice == "Yes"
}

// ConnectionError mirrors LlamaView.connectionError.
func (v *LlamaView) ConnectionError(serverURL, message string) string {
	choice, _ := v.Select("llama.cpp unavailable\n"+serverURL+"\n\n"+message, []string{"Retry", "Close"})
	if choice == "Retry" {
		return "retry"
	}
	return "close"
}

// SearchModels mirrors LlamaView.searchModels. It returns after the search
// component closes and its pending search has stopped.
func (v *LlamaView) SearchModels(search HuggingFaceSearchFunc) (string, bool) {
	type selection struct {
		model string
		ok    bool
	}
	result := make(chan selection, 1)
	v.mu.Lock()
	component := newHuggingFaceSearch(&v.mu, v.requestRender, search, v.searchCache, func(model string, ok bool) {
		result <- selection{model, ok}
	})
	v.setContentLocked(frame("Download model", []tui.Component{tui.NewSpacer(1), component},
		keyHint(tui.KBSelectConfirm, "select")+" • "+keyHint(tui.KBSelectCancel, "back")), component.HandleInput)
	v.mu.Unlock()
	v.requestRender()
	chosen := <-result
	component.tasks.Wait()
	return chosen.model, chosen.ok
}

// ShowStatus mirrors LlamaView.showStatus.
func (v *LlamaView) ShowStatus(title, message string) {
	v.setContent(frame(title, []tui.Component{tui.NewSpacer(1), paddedText(fg("muted", message))}, ""), nil)
}

// Progress mirrors LlamaView.progress: repeated calls share one pending stop
// signal until the user presses cancel or the content changes.
func (v *LlamaView) Progress(state ProgressState) <-chan struct{} {
	v.mu.Lock()
	if v.progressStop == nil {
		v.progressStop = make(chan struct{})
	}
	stop := v.progressStop
	v.showingProgress = true
	v.updateProgressLocked(state)
	v.mu.Unlock()
	v.requestRender()
	return stop
}

// UpdateProgress mirrors LlamaView.updateProgress.
func (v *LlamaView) UpdateProgress(state ProgressState) {
	v.mu.Lock()
	shown := v.updateProgressLocked(state)
	v.mu.Unlock()
	if shown {
		v.requestRender()
	}
}

func (v *LlamaView) updateProgressLocked(state ProgressState) bool {
	if !v.showingProgress {
		return false
	}
	body := []tui.Component{
		paddedText(fg("text", state.Model)),
		tui.NewSpacer(1),
		paddedText(fg("muted", state.Message)),
	}
	if state.Ratio != nil {
		const available = 40
		filled := int(jsRound(max(0, min(1, *state.Ratio)) * available))
		bar := strings.Repeat("█", filled) + strings.Repeat("─", available-filled) + " " + jsNumberString(jsRound(*state.Ratio*100)) + "%"
		body = append(body, paddedText(fg("accent", bar)))
	}
	if state.Detail != "" {
		body = append(body, paddedText(fg("dim", state.Detail)))
	}
	v.content = frame(state.Title, body, keyHint(tui.KBSelectCancel, "stop"))
	v.inputHandler = nil
	return true
}

// HandleInput mirrors LlamaView.handleInput.
func (v *LlamaView) HandleInput(data string) {
	v.mu.Lock()
	if v.progressStop != nil && tui.GetTUIKeybindings().Matches(data, tui.KBSelectCancel) {
		close(v.progressStop)
		v.progressStop = nil
		v.mu.Unlock()
		return
	}
	if v.inputHandler != nil {
		v.inputHandler(data)
	}
	v.mu.Unlock()
	v.requestRender()
}

// Render mirrors LlamaView.render: content lines truncated to width.
func (v *LlamaView) Render(width int) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	lines := v.content.Render(width)
	out := make([]string, len(lines))
	for index, line := range lines {
		if widthx.VisibleWidth(line) > width {
			line = widthx.TruncateToWidth(line, width, "", false)
		}
		out[index] = line
	}
	return out
}

// Invalidate mirrors LlamaView.invalidate.
func (v *LlamaView) Invalidate() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.content.Invalidate()
}

// ShowLlamaUi mirrors showLlamaUi: it shows a LlamaView through ctx.Custom
// and runs the flow on an owned goroutine that ends the view when it returns.
// A flow error is reported through Notify.
func ShowLlamaUi(ctx CommandContext, run func(ui LlamaUi) error) {
	ctx.Custom(func(requestRender func(), done func()) CustomComponent {
		view := NewLlamaView(requestRender)
		go func() {
			defer done()
			if err := run(view); err != nil {
				ctx.Notify(err.Error(), "error")
			}
		}()
		return view
	})
}

// RunWithProgressOptions mirrors runWithProgress's options.
type RunWithProgressOptions[T any] struct {
	Title          string
	Model          string
	InitialMessage string
	CancelTitle    string
	CancelMessage  string
	Run            func(ctx context.Context, update func(LlamaProgress)) (T, error)
	Cancel         func() error
}

// RunWithProgress mirrors runWithProgress: it shows progress while Run works
// and, when the user confirms a stop, calls Cancel and aborts Run with
// "Cancelled". Run is an owned goroutine joined before this returns. Progress updates and view mounting serialize so an initial state cannot overwrite a newer update.
func RunWithProgress[T any](ui LlamaUi, options RunWithProgressOptions[T]) (value T, cancelled bool, err error) {
	runCtx, abort := context.WithCancelCause(context.Background())
	defer abort(nil)
	var stateMu sync.Mutex
	state := ProgressState{Title: options.Title, Model: options.Model}
	state.Message = options.InitialMessage
	showProgress := func() <-chan struct{} {
		stateMu.Lock()
		defer stateMu.Unlock()
		return ui.Progress(state)
	}
	settled := make(chan struct{})
	var result T
	var runErr error
	go func() {
		defer close(settled)
		result, runErr = options.Run(runCtx, func(progress LlamaProgress) {
			stateMu.Lock()
			defer stateMu.Unlock()
			state.assign(progress)
			ui.UpdateProgress(state)
		})
	}()
	isSettled := func() bool {
		select {
		case <-settled:
			return true
		default:
			return false
		}
	}
	for !isSettled() {
		select {
		case <-settled:
			continue
		case <-showProgress():
		}
		if !ui.Confirm(options.CancelTitle, options.CancelMessage) || isSettled() {
			continue
		}
		cancelErr := options.Cancel()
		abort(errors.New("Cancelled"))
		<-settled
		if cancelErr != nil {
			return value, false, cancelErr
		}
		return value, true, nil
	}
	if runErr != nil {
		return value, false, runErr
	}
	return result, false, nil
}
