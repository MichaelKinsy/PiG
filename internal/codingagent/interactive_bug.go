package codingagent

import (
	"context"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// bugReportLoaderFrame is the spinner cadence while /bug writes a summary.
// upstream: tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
const bugReportLoaderFrame = 80 * time.Millisecond

// bugReportSummarizer is the session capability /bug uses for the optional
// summary. coding.Session implements it.
type bugReportSummarizer interface {
	SummarizeForBugReport(ctx context.Context, hint string) (string, error)
}

func (m *InteractiveMode) bugReportInputs() (BugReportInputs, error) {
	inputs := BugReportInputs{
		Version:       InstalledPigVersion,
		Model:         m.opts.Model,
		ThinkingLevel: m.thinkingLevel,
	}
	if m.agent != nil {
		inputs.MessageCount = len(m.agent.Messages())
	}
	if m.opts.Model != nil {
		inputs.Provider = m.opts.ModelRegistry.BugReportProviderInfo(m.opts.Model.ProviderMeta.ProviderID)
	}
	if m.newRunner != nil {
		inputs.ExtensionPaths = m.newRunner.ExtensionPaths()
	}
	if m.opts.SubprocessHost != nil {
		inputs.ExtensionErrors = m.opts.SubprocessHost.LoadErrors()
	}
	if m.opts.SettingsManager != nil {
		inputs.GlobalSettings = m.opts.SettingsManager.GetGlobalSettings()
		inputs.ProjectSettings = m.opts.SettingsManager.GetProjectSettings()
	}
	return inputs, nil
}

func (m *InteractiveMode) bugReportProviderName() string {
	if m.opts.Model == nil {
		return ""
	}
	providerID := m.opts.Model.ProviderMeta.ProviderID
	if m.opts.ModelRegistry == nil {
		return providerID
	}
	return m.opts.ModelRegistry.GetProviderDisplayName(providerID)
}

// summarizeForBugReport runs the session summary behind a cancellable
// bordered loader in the editor slot, as upstream showLoader does. Esc
// cancels the request.
func (m *InteractiveMode) summarizeForBugReport(modelName, hint string) (string, bool, error) {
	summarizer, ok := m.opts.SessionHandle.(bugReportSummarizer)
	if !ok || m.layout == nil {
		return "", false, errNoBugReportSummarizer
	}
	loader := tui.NewBorderedLoader("Writing summary with "+modelName+"...", true)
	ctx, cancel := context.WithCancel(loader.CancellableContext().Context())
	defer cancel()

	m.editorContainer.SetChildren(loader)
	m.tuiInst.Render()
	defer func() {
		loader.Dispose()
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	type result struct {
		summary string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		summary, err := summarizer.SummarizeForBugReport(ctx, hint)
		done <- result{summary, err}
	}()
	ticker := time.NewTicker(bugReportLoaderFrame)
	defer ticker.Stop()
	for {
		select {
		case buf := <-inputCh:
			for _, chunk := range dropKeyReleases(loader, []string{string(buf)}) {
				loader.HandleInput(chunk)
			}
			m.tuiInst.Render()
		case <-ticker.C:
			loader.NextFrame()
			m.tuiInst.Render()
		case outcome := <-done:
			if ctx.Err() != nil {
				return "", true, nil
			}
			return outcome.summary, false, outcome.err
		}
	}
}
