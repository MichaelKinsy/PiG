package codingagent

// interactive_crash.go wires the crash log (crash_log.go) into interactive
// mode the way upstream InteractiveMode does: UI panics and fatal create,
// resume, and import failures are recorded, and the next start announces them.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// ErrInteractiveCrashed reports that Run recovered a crash it already
// printed and recorded; the caller exits 1 without printing it again.
var ErrInteractiveCrashed = errors.New("interactive mode crashed")

// showCrashNotice announces the newest unannounced crash. Mirrors the
// takeUnnotifiedCrash notice in upstream InteractiveMode.init.
func (m *InteractiveMode) showCrashNotice() {
	if m.opts.AgentDir == "" {
		return
	}
	if crash, ok := TakeUnnotifiedCrash(CrashLogPath(m.opts.AgentDir), time.Now()); ok {
		m.showWarning(crashNotice(crash))
	}
}

// crashMessage mirrors upstream `error.message || error.name` for errors and
// String(value) otherwise.
func crashMessage(value any) string {
	if err, ok := value.(error); ok {
		if message := err.Error(); message != "" {
			return message
		}
		return fmt.Sprintf("%T", err)
	}
	return fmt.Sprint(value)
}

// crashSessionFile returns the current session file, if any.
func (m *InteractiveMode) crashSessionFile() string {
	if session := m.currentSession(); session != nil {
		return session.Path()
	}
	return ""
}

// recordCrash persists a crash so the next start can point at /bug. It reports
// false when nothing was written. Mirrors upstream InteractiveMode.recordCrash.
func (m *InteractiveMode) recordCrash(kind string, value any, stack string) bool {
	if m.opts.AgentDir == "" {
		return false
	}
	cwd := m.opts.CWD
	if session := m.currentSession(); session != nil && session.CWD() != "" {
		cwd = session.CWD()
	}
	_, ok := RecordCrash(CrashInput{
		Kind:        kind,
		Message:     crashMessage(value),
		Stack:       stack,
		SessionFile: m.crashSessionFile(),
		CWD:         cwd,
		Version:     m.opts.AppVersion,
	}, CrashLogPath(m.opts.AgentDir), time.Now())
	return ok
}

// crashExtensionMetadata snapshots loaded extensions and their provenance for
// stack attribution. SubprocessHost's concrete implementation supplies the
// registered set; resource metadata retains authored package roots.
func (m *InteractiveMode) crashExtensionMetadata() []ExtensionStackMetadata {
	metadata := make([]ExtensionStackMetadata, 0)
	seen := make(map[string]struct{})
	appendExtension := func(path, resolvedPath string, sourceInfo ResourceSourceInfo) {
		key := path + "\x00" + resolvedPath + "\x00" + sourceInfo.Source + "\x00" + sourceInfo.BaseDir
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		metadata = append(metadata, ExtensionStackMetadata{Path: path, ResolvedPath: resolvedPath, SourceInfo: sourceInfo})
	}
	if provider, ok := m.opts.SubprocessHost.(interface{ Extensions() []extension.Extension }); ok {
		for _, loaded := range provider.Extensions() {
			appendExtension(loaded.Path, loaded.ResolvedPath, resourceSourceInfoValue(loaded.SourceInfo))
		}
	}
	for _, loaded := range m.opts.BuiltinExtensions {
		appendExtension(loaded.Path, loaded.ResolvedPath, resourceSourceInfoValue(loaded.SourceInfo))
	}
	paths := make([]string, 0, len(m.resourceSourceInfo))
	for path, info := range m.resourceSourceInfo {
		if info.ResourceType == "extensions" && info.Enabled {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		info := m.resourceSourceInfo[path]
		resolvedPath := info.Path
		if resolvedPath == "" {
			resolvedPath = path
		}
		appendExtension(path, resolvedPath, info)
	}
	return metadata
}

func resourceSourceInfoValue(value extension.SourceInfo) ResourceSourceInfo {
	if info, ok := value.(ResourceSourceInfo); ok {
		return info
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ResourceSourceInfo{}
	}
	var info ResourceSourceInfo
	if json.Unmarshal(data, &info) != nil {
		return ResourceSourceInfo{}
	}
	return info
}

func (m *InteractiveMode) crashExtensionHint(stack string) string {
	return FormatCrashExtensionHint(FindExtensionStackMatches(stack, m.crashExtensionMetadata()))
}

// handleFatalRuntimeError reports a runtime replacement failure and asks the
// input loop to exit with status 1 after terminal restoration.
func (m *InteractiveMode) handleFatalRuntimeError(prefix string, value any) error {
	message := crashMessage(value)
	stack := string(debug.Stack())
	m.showError(prefix + ": " + message)
	if hint := m.crashExtensionHint(stack); hint != "" {
		m.appendChatBlock(tui.NewText(tui.ActiveTheme().Warning + hint + "\x1b[0m"))
	}
	if m.recordCrash("fatal_error", value, stack) {
		m.appendChatBlock(tui.NewText(dim(crashReportInstructions(m.crashSessionFile()))))
	}
	m.fatalRuntime.Store(true)
	m.requestExit.Store(true)
	return fmt.Errorf("%w: %s: %s", ErrInteractiveCrashed, prefix, message)
}

func (m *InteractiveMode) requestedExitError() error {
	if m.fatalRuntime.Load() {
		return ErrInteractiveCrashed
	}
	return nil
}

// uncaughtCrash reports a crash recovered on the UI goroutine after the
// terminal has been restored. Mirrors upstream InteractiveMode.uncaughtCrash.
func (m *InteractiveMode) uncaughtCrash(value any, stack []byte, stderr io.Writer) {
	_, _ = fmt.Fprintf(stderr, "%s exiting due to uncaughtException:\n%s\n%s", AppName, uncaughtValueText(value), stack)
	if hint := m.crashExtensionHint(string(stack)); hint != "" {
		_, _ = fmt.Fprintf(stderr, "\n%s\n", hint)
	}
	if m.recordCrash("uncaught_exception", value, string(stack)) {
		_, _ = fmt.Fprintf(stderr, "\n%s\n", crashReportInstructions(m.crashSessionFile()))
	}
}

// uncaughtValueText formats a recovered value for the uncaughtException
// report. The renderer overflow is upstream's thrown Error, which Node's
// console.error prints as "Error: <message>" before its stack.
func uncaughtValueText(value any) string {
	var overflow *tui.RenderOverflowError
	if err, ok := value.(error); ok && errors.As(err, &overflow) {
		return "Error: " + overflow.Error()
	}
	return fmt.Sprint(value)
}

// forwardRenderCrash re-raises a renderer overflow recovered off the owner
// loop on the owner loop, where Run's uncaughtException handler restores the
// terminal, records the crash, and exits 1. Upstream renders on its single
// event loop, so the throw always reaches that handler. Other panics
// propagate unchanged.
func (m *InteractiveMode) forwardRenderCrash(value any) {
	var overflow *tui.RenderOverflowError
	err, ok := value.(error)
	if !ok || !errors.As(err, &overflow) {
		panic(value)
	}
	raise := func() { panic(value) }
	if !m.postUITask(raise) {
		go func() { _ = m.postToMain(m.runCtx, raise) }()
	}
}
