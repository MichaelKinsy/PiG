package llama

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Ports packages/coding-agent/src/extensions/llama/index.ts: the /llama
// command registered next to the provider.

// CommandName and CommandDescription mirror the pi.registerCommand call.
const (
	CommandName        = "llama"
	CommandDescription = "Manage llama.cpp router models"
)

// CommandContext mirrors the ExtensionCommandContext members /llama uses.
// Notify may be called from the flow goroutine while Custom is showing.
type CommandContext struct {
	Ctx    context.Context
	Mode   string
	Notify func(message, notifyType string)
	// Custom shows the component the factory returns in the editor slot and
	// blocks until the factory's done callback runs, like ctx.ui.custom.
	Custom func(factory func(requestRender, done func()) CustomComponent)
}

const catalogSyncTimeout = 15 * time.Second

func modelIsLoaded(model LlamaModelInfo) bool {
	return model.Status.Value == LlamaModelStatusLoaded || model.Status.Value == LlamaModelStatusSleeping
}

func isConnectionError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "fetch failed") || strings.Contains(message, "timeout") || strings.Contains(message, "network")
}

func connectionErrorMessage(err error) string {
	if isConnectionError(err) {
		return "Could not connect to the server."
	}
	return err.Error()
}

// parseHuggingFaceModel splits owner/repository[:quant] at the first colon
// after the first slash.
func parseHuggingFaceModel(value string) (repository, quantization string) {
	start := strings.Index(value, "/") + 1
	colon := strings.Index(value[start:], ":")
	if colon < 0 {
		return value, ""
	}
	return value[:start+colon], value[start+colon+1:]
}

func (h *Host) configuredClient(ctx CommandContext) (*LlamaClient, error) {
	result, err := h.GetProviderAuth(ctx.Ctx)
	if err != nil {
		return nil, err
	}
	if result == nil {
		ctx.Notify("Configure llama.cpp with /login "+LlamaProviderID, "warning")
		return nil, nil
	}
	serverURL := result.Env["LLAMA_BASE_URL"]
	if serverURL == "" {
		serverURL = result.Auth.BaseURL
	}
	normalized, err := NormalizeLlamaServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	return NewLlamaClient(normalized, result.Auth.APIKey)
}

// commandSession carries one /llama invocation's collaborators.
type commandSession struct {
	host   *Host
	ctx    CommandContext
	ui     LlamaUi
	client *LlamaClient
}

func (s *commandSession) notify(message string) { s.ctx.Notify(message, "info") }

// syncCatalog mirrors index.ts syncCatalog; provided distinguishes a passed
// empty catalog from an omitted one.
func (s *commandSession) syncCatalog(catalog []LlamaModelInfo, provided bool) ([]LlamaModelInfo, error) {
	ctx, cancel := context.WithTimeoutCause(s.ctx.Ctx, catalogSyncTimeout, timeoutError{})
	defer cancel()
	current := catalog
	if !provided {
		var err error
		if current, err = s.client.List(ctx, false); err != nil {
			return nil, err
		}
	}
	s.host.controller.SetCatalog(current, s.client.ServerURL, false)
	s.host.SyncRegistration(ctx)
	// /llama already contacted the configured llama.cpp server, so keep this refresh live even in PI_OFFLINE.
	result := s.host.Refresh(ctx, true)
	if result.Aborted {
		return nil, errors.New("Model catalog refresh timed out.")
	}
	if result.Err != nil {
		return nil, result.Err
	}
	return current, nil
}

func (s *commandSession) restoreLoaded(loaded []LlamaModelInfo) error {
	s.notify("Restoring previously loaded models")
	for _, model := range loaded {
		if _, err := s.client.LoadAndWait(s.ctx.Ctx, model.ID, func(LlamaProgress) {}); err != nil {
			return err
		}
	}
	_, err := s.syncCatalog(nil, false)
	return err
}

func (s *commandSession) loadModel(catalog []LlamaModelInfo, target LlamaModelInfo) error {
	var loaded []LlamaModelInfo
	for _, model := range catalog {
		if model.ID != target.ID && modelIsLoaded(model) {
			loaded = append(loaded, model)
		}
	}
	replace := false
	if len(loaded) > 0 {
		verb := "s are"
		if len(loaded) == 1 {
			verb = " is"
		}
		choice, ok := s.ui.Select(fmt.Sprintf("%d model%s loaded", len(loaded), verb), []string{"Unload all and load", "Keep loaded and load", "Cancel"})
		if !ok || choice == "Cancel" {
			return nil
		}
		replace = choice == "Unload all and load"
	}
	if replace {
		for _, model := range loaded {
			if err := s.client.UnloadAndWait(s.ctx.Ctx, model.ID); err != nil {
				return err
			}
		}
	}
	err := s.loadAfterReplace(target, replace, loaded)
	if err != nil && replace {
		// Preserve the original load error.
		_ = s.restoreLoaded(loaded)
	}
	return err
}

func (s *commandSession) loadAfterReplace(target LlamaModelInfo, replace bool, loaded []LlamaModelInfo) error {
	_, cancelled, err := RunWithProgress(s.ui, RunWithProgressOptions[LlamaModelInfo]{
		Title:          "Loading model",
		Model:          target.ID,
		InitialMessage: "Starting…",
		CancelTitle:    "Stop loading?",
		CancelMessage:  target.ID,
		Run: func(ctx context.Context, update func(LlamaProgress)) (LlamaModelInfo, error) {
			return s.client.LoadAndWait(ctx, target.ID, update)
		},
		Cancel: func() error { return s.client.Unload(s.ctx.Ctx, target.ID) },
	})
	if err != nil {
		return err
	}
	if cancelled {
		if replace {
			return s.restoreLoaded(loaded)
		}
		return nil
	}
	refreshed, err := s.syncCatalog(nil, false)
	if err != nil {
		return err
	}
	if model := findModel(refreshed, target.ID); model != nil && model.Status.Value == LlamaModelStatusLoaded {
		s.notify("Loaded " + target.ID)
	} else {
		s.notify("Load started for " + target.ID)
	}
	return nil
}

func (s *commandSession) unloadModel(model LlamaModelInfo) error {
	if !s.ui.Confirm("Unload model?", model.ID) {
		return nil
	}
	if err := s.client.UnloadAndWait(s.ctx.Ctx, model.ID); err != nil {
		return err
	}
	if _, err := s.syncCatalog(nil, false); err != nil {
		return err
	}
	s.notify("Unloaded " + model.ID)
	return nil
}

func quantizationOptions(quantizations []HuggingFaceQuantization) []string {
	options := make([]string, 0, len(quantizations))
	for _, entry := range quantizations {
		var detail []string
		if entry.Size != nil {
			detail = append(detail, FormatBytes(*entry.Size))
		}
		if entry.Name == "Q4_K_M" {
			detail = append(detail, "recommended")
		}
		if len(detail) > 0 {
			options = append(options, entry.Name+" · "+strings.Join(detail, " · "))
		} else {
			options = append(options, entry.Name)
		}
	}
	return options
}

func (s *commandSession) downloadModel() error {
	huggingFace := NewHuggingFaceClient(FindHuggingFaceToken(os.Getenv), s.host.huggingFaceURL)
	selected, ok := s.ui.SearchModels(huggingFace.Search)
	if !ok {
		return nil
	}
	repository, quantization := parseHuggingFaceModel(selected)
	s.ui.ShowStatus("Loading model details", repository)
	details, err := huggingFace.Details(s.ctx.Ctx, repository)
	if err != nil {
		return err
	}
	if details.Gated != "" {
		approval := "Accept the access terms"
		if details.Gated == "manual" {
			approval = "Manual approval is required"
		}
		choice, _ := s.ui.Select(fmt.Sprintf("Hugging Face access required\n%s\n\n%s at:\nhttps://huggingface.co/%s\n\nThe llama.cpp server needs HF_TOKEN with access.", details.ID, approval, details.ID),
			[]string{"Continue", "Back"})
		if choice != "Continue" {
			return nil
		}
	}
	if quantization == "" && len(details.Quantizations) > 0 {
		options := quantizationOptions(details.Quantizations)
		choice, ok := s.ui.Select("Select quantization\n"+details.ID, options)
		if !ok {
			return nil
		}
		quantization = details.Quantizations[slices.Index(options, choice)].Name
	}
	model := details.ID
	if quantization != "" {
		model += ":" + quantization
	}
	models, cancelled, err := RunWithProgress(s.ui, RunWithProgressOptions[[]LlamaModelInfo]{
		Title:          "Downloading model",
		Model:          model,
		InitialMessage: "Starting…",
		CancelTitle:    "Stop download?",
		CancelMessage:  model,
		Run: func(ctx context.Context, update func(LlamaProgress)) ([]LlamaModelInfo, error) {
			return s.client.DownloadAndWait(ctx, model, update)
		},
		Cancel: func() error { return s.client.Unload(s.ctx.Ctx, model) },
	})
	if err != nil || cancelled {
		return err
	}
	if _, err := s.syncCatalog(models, true); err != nil {
		return err
	}
	s.notify("Downloaded " + model)
	return nil
}

func (s *commandSession) readCatalog() ([]LlamaModelInfo, bool) {
	for {
		catalog, err := s.syncCatalog(nil, false)
		if err == nil {
			return catalog, true
		}
		if s.ui.ConnectionError(s.client.ServerURL, connectionErrorMessage(err)) == "close" {
			return nil, false
		}
	}
}

func (s *commandSession) runAction(catalog []LlamaModelInfo, action LlamaManagerAction) error {
	switch {
	case action.Type == LlamaManagerActionDownload:
		return s.downloadModel()
	case modelIsLoaded(action.Model):
		return s.unloadModel(action.Model)
	case action.Model.Status.Value == LlamaModelStatusUnloaded:
		return s.loadModel(catalog, action.Model)
	}
	s.ctx.Notify(fmt.Sprintf("%s is %s", action.Model.ID, action.Model.Status.Value), "warning")
	return nil
}

func (s *commandSession) manage(ui LlamaUi) error {
	s.ui = ui
	catalog, ok := s.readCatalog()
	if !ok {
		return nil
	}
	for {
		action := ui.ShowModels(s.client.ServerURL, catalog)
		if action.Type == LlamaManagerActionClose {
			return nil
		}
		actionErr := s.runAction(catalog, action)
		refreshed, ok := s.readCatalog()
		if !ok {
			return nil
		}
		catalog = refreshed
		if actionErr != nil && !isConnectionError(actionErr) {
			s.ctx.Notify(actionErr.Error(), "error")
		}
	}
}

// HandleCommand mirrors the /llama command handler.
func (h *Host) HandleCommand(ctx CommandContext) error {
	if ctx.Mode != "tui" {
		ctx.Notify("/llama is available in interactive mode", "warning")
		return nil
	}
	client, err := h.configuredClient(ctx)
	if err != nil || client == nil {
		return err
	}
	session := &commandSession{host: h, ctx: ctx, client: client}
	ShowLlamaUi(ctx, session.manage)
	return nil
}
