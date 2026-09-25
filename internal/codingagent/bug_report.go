package codingagent

// bug_report.go ports the export side of upstream core/bug-report.ts: the
// report metadata, redaction, failed-turn diagnostics, the bundle files, and
// the zip archive. PiG never uploads a report (D62); see slash_bug.go.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/ai"
)

// BugReportCustomEntryType is the session custom-entry type recorded after a
// report is written. It matches upstream so Pi and PiG read the same history.
const BugReportCustomEntryType = "pi.bug-report"

const (
	bugReportSchemaVersion = 1
	bugReportRedacted      = "<redacted>"
	// BugReportIssueURL is the PiG issue form that /bug links to. PiG
	// reports go to the PiG repository, never to Pi's report gateway (D62).
	BugReportIssueURL = "https://github.com/MichaelKinsy/PiG/issues/new"
	// bugReportIssueTemplate is .github/ISSUE_TEMPLATE/bug.yml.
	bugReportIssueTemplate = "bug.yml"
	// bugReportIssueTitleRunes bounds the title taken from the description.
	bugReportIssueTitleRunes = 72
)

var (
	bugReportSensitiveKey = regexp.MustCompile(`(?i)(?:^|[-_])(api[-_]?key|secret|token|password|passwd|credential|authorization|cookie)(?:$|[-_])`)
	bugReportCamelBreak   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	bugReportNestedURL    = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*:)([a-z][a-z0-9+.-]*://.*)$`)
	// WHATWG special schemes serialize an empty path as "/".
	bugReportSpecialSchemes = []string{"ftp", "file", "http", "https", "ws", "wss"}
)

func isBugReportSensitiveKey(key string) bool {
	return bugReportSensitiveKey.MatchString(bugReportCamelBreak.ReplaceAllString(key, "${1}_${2}"))
}

// RedactBugReportURL strips credentials and secret-looking query parameters
// from an absolute URL, including a URL nested after a scheme prefix such as
// "git:https://...". Other strings are returned unchanged. Mirrors upstream
// redactUrl.
func RedactBugReportURL(value string) string {
	if nested := bugReportNestedURL.FindStringSubmatch(value); nested != nil {
		return nested[1] + RedactBugReportURL(nested[2])
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Opaque != "" {
		return value
	}
	changed := false
	if parsed.User != nil {
		parsed.User = nil
		changed = true
	}
	if query, queryChanged := redactBugReportQuery(parsed.RawQuery); queryChanged {
		parsed.RawQuery = query
		changed = true
	}
	if !changed {
		return value
	}
	if parsed.Path == "" && parsed.Host != "" && slices.Contains(bugReportSpecialSchemes, strings.ToLower(parsed.Scheme)) {
		parsed.Path = "/"
	}
	return parsed.String()
}

// redactBugReportQuery replaces the first value of each sensitive query key
// and drops its later duplicates, then reserializes every pair as
// application/x-www-form-urlencoded, as URLSearchParams.set does.
func redactBugReportQuery(raw string) (string, bool) {
	if raw == "" {
		return raw, false
	}
	type pair struct{ key, value string }
	var pairs []pair
	for part := range strings.SplitSeq(raw, "&") {
		if part == "" {
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		key, _ = url.QueryUnescape(key)
		value, _ = url.QueryUnescape(value)
		pairs = append(pairs, pair{key, value})
	}
	changed := false
	redacted := make(map[string]bool)
	out := pairs[:0]
	for _, p := range pairs {
		if isBugReportSensitiveKey(p.key) {
			changed = true
			if redacted[p.key] {
				continue
			}
			redacted[p.key] = true
			p.value = bugReportRedacted
		}
		out = append(out, p)
	}
	if !changed {
		return raw, false
	}
	parts := make([]string, len(out))
	for i, p := range out {
		parts[i] = formURLEncode(p.key) + "=" + formURLEncode(p.value)
	}
	return strings.Join(parts, "&"), true
}

// formURLEncode is the WHATWG application/x-www-form-urlencoded byte
// serializer: alphanumerics and *-._ stay, space becomes +, all else is
// percent-encoded.
func formURLEncode(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// RedactBugReportJSON copies a JSON-encodable value, replacing the value of
// every sensitive key with "<redacted>" and redacting URLs in strings.
// Mirrors upstream redactJsonValue.
func RedactBugReportJSON(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return redactBugReportNode(decoded), nil
}

func redactBugReportNode(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if child != nil && isBugReportSensitiveKey(key) {
				typed[key] = bugReportRedacted
				continue
			}
			typed[key] = redactBugReportNode(child)
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = redactBugReportNode(child)
		}
		return typed
	case string:
		return RedactBugReportURL(typed)
	default:
		return value
	}
}

func redactBugReportSettings(settings Settings) (any, error) {
	redacted, err := RedactBugReportJSON(settings)
	if err != nil {
		return nil, err
	}
	if object, ok := redacted.(map[string]any); ok {
		delete(object, "trackingId")
	}
	return redacted, nil
}

// BugReportEnvironment describes the running PiG process without secrets.
type BugReportEnvironment struct {
	Version   string               `json:"version"`
	UserAgent string               `json:"userAgent"`
	Runtime   string               `json:"runtime"`
	Platform  string               `json:"platform"`
	Arch      string               `json:"arch"`
	OSRelease string               `json:"osRelease"`
	OSVersion string               `json:"osVersion"`
	Shell     *string              `json:"shell"`
	Terminal  BugReportTerminalEnv `json:"terminal"`
	// PiEnvironmentVariables lists PI_* names only; values never leave the
	// machine. PigEnvironmentVariables lists PiG's own PIG_* names.
	PiEnvironmentVariables  []string `json:"piEnvironmentVariables"`
	PigEnvironmentVariables []string `json:"pigEnvironmentVariables"`
}

// BugReportTerminalEnv records terminal identification variables.
type BugReportTerminalEnv struct {
	Term           *string `json:"term"`
	Program        *string `json:"program"`
	ProgramVersion *string `json:"programVersion"`
	Colorterm      *string `json:"colorterm"`
	Tmux           bool    `json:"tmux"`
	SSH            bool    `json:"ssh"`
	CI             bool    `json:"ci"`
}

func optionalEnv(getenv func(string) string, name string) *string {
	if value := getenv(name); value != "" {
		return &value
	}
	return nil
}

// nodePlatform and nodeArch report the same names as Pi's process.platform
// and process.arch, so PiG and Pi reports describe a machine identically.
func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}

func codingAgentUserAgent(version, platform, release, arch string) string {
	// pig divergence (D65): use the one owner-approved PiG product identity
	// instead of coding-agent's separate runtime-based pi/<version> shape.
	return AppName + "/" + version + " (" + platform + " " + release + "; " + arch + ")"
}

func collectBugReportEnvironment(version string, getenv func(string) string, environ []string) BugReportEnvironment {
	release, osVersion := bugReportOSRelease()
	var shell *string
	if value := getenv("SHELL"); value != "" {
		name := value[strings.LastIndexAny(value, `\/`)+1:]
		if name != "" {
			shell = &name
		}
	}
	env := BugReportEnvironment{
		Version:   version,
		UserAgent: codingAgentUserAgent(version, nodePlatform(runtime.GOOS), release, nodeArch(runtime.GOARCH)),
		Runtime:   "go/" + strings.TrimPrefix(runtime.Version(), "go"),
		Platform:  nodePlatform(runtime.GOOS),
		Arch:      nodeArch(runtime.GOARCH),
		OSRelease: release,
		OSVersion: osVersion,
		Shell:     shell,
		Terminal: BugReportTerminalEnv{
			Term:           optionalEnv(getenv, "TERM"),
			Program:        optionalEnv(getenv, "TERM_PROGRAM"),
			ProgramVersion: optionalEnv(getenv, "TERM_PROGRAM_VERSION"),
			Colorterm:      optionalEnv(getenv, "COLORTERM"),
			Tmux:           getenv("TMUX") != "",
			SSH:            getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != "",
			CI:             getenv("CI") != "",
		},
		PiEnvironmentVariables:  []string{},
		PigEnvironmentVariables: []string{},
	}
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.HasPrefix(name, "PI_"):
			env.PiEnvironmentVariables = append(env.PiEnvironmentVariables, name)
		case strings.HasPrefix(name, "PIG_"):
			env.PigEnvironmentVariables = append(env.PigEnvironmentVariables, name)
		}
	}
	slices.Sort(env.PiEnvironmentVariables)
	slices.Sort(env.PigEnvironmentVariables)
	return env
}

// BugReportModel describes the current model without credentials.
type BugReportModel struct {
	Provider         string              `json:"provider"`
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	API              string              `json:"api"`
	BaseURL          string              `json:"baseUrl"`
	Reasoning        bool                `json:"reasoning"`
	Input            []string            `json:"input"`
	ContextWindow    int                 `json:"contextWindow"`
	MaxTokens        int                 `json:"maxTokens"`
	SamplingParams   any                 `json:"samplingParams"`
	Compat           any                 `json:"compat"`
	ThinkingLevelMap ai.ThinkingLevelMap `json:"thinkingLevelMap"`
	HeaderNames      []string            `json:"headerNames"`
}

func describeBugReportModel(model *ai.Model) (*BugReportModel, error) {
	if model == nil {
		return nil, nil
	}
	described := &BugReportModel{
		Provider:         model.ProviderMeta.ProviderID,
		ID:               model.ID,
		Name:             model.DisplayName,
		API:              string(model.ProviderMeta.API),
		BaseURL:          RedactBugReportURL(model.ProviderMeta.BaseURL),
		Reasoning:        model.ProviderMeta.Reasoning,
		Input:            slices.Clone(model.Input),
		ContextWindow:    model.Capabilities.ContextWindow,
		MaxTokens:        model.Capabilities.MaxOutputTokens,
		ThinkingLevelMap: model.ThinkingLevelMap,
		HeaderNames:      sortedKeys(model.ProviderMeta.Headers),
	}
	if described.Input == nil {
		described.Input = []string{}
	}
	var err error
	if len(model.SamplingParams) > 0 {
		if described.SamplingParams, err = RedactBugReportJSON(model.SamplingParams); err != nil {
			return nil, err
		}
	}
	if model.ProviderMeta.Compat != nil {
		if described.Compat, err = RedactBugReportJSON(model.ProviderMeta.Compat); err != nil {
			return nil, err
		}
	}
	return described, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// BugReportProvider describes the current model's provider without
// credentials.
type BugReportProvider struct {
	ID                    string              `json:"id"`
	Name                  string              `json:"name"`
	BaseURL               *string             `json:"baseUrl"`
	HeaderNames           []string            `json:"headerNames"`
	AuthStatus            BugReportAuthStatus `json:"authStatus"`
	UsingOAuth            bool                `json:"usingOAuth"`
	RegisteredByExtension bool                `json:"registeredByExtension"`
}

// BugReportAuthStatus mirrors upstream's provider auth status record.
type BugReportAuthStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Label      string `json:"label,omitempty"`
}

// BugReportProviderInfo describes providerID from models.json and extension
// registrations. It returns nil when the registry is nil.
func (r *ModelRegistry) BugReportProviderInfo(providerID string) *BugReportProvider {
	if r == nil || providerID == "" {
		return nil
	}
	status := r.GetProviderAuthStatus(providerID)
	r.mu.RLock()
	config, configured := providerConfig{}, false
	if r.config != nil {
		config, configured = r.config.Providers[providerID]
	}
	dynamic, registered := r.dynamic[providerID]
	r.mu.RUnlock()
	if registered {
		config = dynamic
	}
	info := &BugReportProvider{
		ID:                    providerID,
		Name:                  r.GetProviderDisplayName(providerID),
		HeaderNames:           sortedKeys(config.Headers),
		AuthStatus:            BugReportAuthStatus{Configured: status.Configured, Source: string(status.Source), Label: status.Label},
		UsingOAuth:            r.hasStoredOAuth(providerID),
		RegisteredByExtension: registered,
	}
	if (configured || registered) && config.BaseURL != "" {
		baseURL := RedactBugReportURL(config.BaseURL)
		info.BaseURL = &baseURL
	}
	return info
}

// BugReportExtension describes one loaded extension by its resolved path.
type BugReportExtension struct {
	Path string `json:"path"`
}

// BugReportExtensionError is one extension load failure. PiG's subprocess
// host reports failures as messages without a separate path.
type BugReportExtensionError struct {
	Error string `json:"error"`
}

// BugReportSessionInfo records what the report shares about the session.
type BugReportSessionInfo struct {
	ID              string `json:"id"`
	Included        bool   `json:"included"`
	SummaryIncluded bool   `json:"summaryIncluded"`
	MessageCount    int    `json:"messageCount"`
	CWD             string `json:"cwd,omitempty"`
}

// BugReportSettings holds the redacted global and project settings.
type BugReportSettings struct {
	Global  any `json:"global"`
	Project any `json:"project"`
}

// BugReportMetadata is report.json. Field order follows upstream.
type BugReportMetadata struct {
	SchemaVersion   int                       `json:"schemaVersion"`
	ID              string                    `json:"id"`
	CreatedAt       string                    `json:"createdAt"`
	Hint            *string                   `json:"hint"`
	Environment     BugReportEnvironment      `json:"environment"`
	Session         BugReportSessionInfo      `json:"session"`
	Model           *BugReportModel           `json:"model"`
	Provider        *BugReportProvider        `json:"provider"`
	ThinkingLevel   string                    `json:"thinkingLevel"`
	Extensions      []BugReportExtension      `json:"extensions"`
	ExtensionErrors []BugReportExtensionError `json:"extensionErrors"`
	Settings        BugReportSettings         `json:"settings"`
}

// BugReportInputs is the process state a report describes. The interactive
// host fills it; tests construct it directly.
type BugReportInputs struct {
	Version         string
	SessionID       string
	CWD             string
	MessageCount    int
	Model           *ai.Model
	Provider        *BugReportProvider
	ThinkingLevel   string
	ExtensionPaths  []string
	ExtensionErrors []string
	GlobalSettings  Settings
	ProjectSettings Settings
	Entries         []SessionEntry
	Branch          []SessionEntry
	Header          SessionHeader
}

// BugReportOptions are the user's consent choices.
type BugReportOptions struct {
	Hint           string
	IncludeSession bool
}

func isoTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// CollectBugReportMetadata builds report.json. Mirrors upstream
// collectBugReportMetadata.
func CollectBugReportMetadata(inputs BugReportInputs, options BugReportOptions, summaryIncluded bool, now time.Time) (BugReportMetadata, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return BugReportMetadata{}, err
	}
	var hint *string
	if trimmed := strings.TrimSpace(options.Hint); trimmed != "" {
		hint = &trimmed
	}
	model, err := describeBugReportModel(inputs.Model)
	if err != nil {
		return BugReportMetadata{}, err
	}
	global, err := redactBugReportSettings(inputs.GlobalSettings)
	if err != nil {
		return BugReportMetadata{}, err
	}
	project, err := redactBugReportSettings(inputs.ProjectSettings)
	if err != nil {
		return BugReportMetadata{}, err
	}
	metadata := BugReportMetadata{
		SchemaVersion: bugReportSchemaVersion,
		ID:            id.String(),
		CreatedAt:     isoTimestamp(now),
		Hint:          hint,
		Environment:   collectBugReportEnvironment(inputs.Version, os.Getenv, os.Environ()),
		Session: BugReportSessionInfo{
			ID:              inputs.SessionID,
			Included:        options.IncludeSession,
			SummaryIncluded: summaryIncluded,
			MessageCount:    inputs.MessageCount,
		},
		Model:           model,
		ThinkingLevel:   inputs.ThinkingLevel,
		Extensions:      make([]BugReportExtension, 0, len(inputs.ExtensionPaths)),
		ExtensionErrors: make([]BugReportExtensionError, 0, len(inputs.ExtensionErrors)),
		Settings:        BugReportSettings{Global: global, Project: project},
	}
	if options.IncludeSession {
		metadata.Session.CWD = inputs.CWD
	}
	if inputs.Model != nil {
		metadata.Provider = inputs.Provider
	}
	for _, path := range inputs.ExtensionPaths {
		metadata.Extensions = append(metadata.Extensions, BugReportExtension{Path: path})
	}
	for _, message := range inputs.ExtensionErrors {
		metadata.ExtensionErrors = append(metadata.ExtensionErrors, BugReportExtensionError{Error: message})
	}
	return metadata, nil
}

// BugReportAssistantDiagnostic is one failed or diagnosed assistant turn.
type BugReportAssistantDiagnostic struct {
	EntryID       string                          `json:"entryId"`
	Timestamp     string                          `json:"timestamp"`
	Provider      string                          `json:"provider"`
	Model         string                          `json:"model"`
	API           string                          `json:"api"`
	StopReason    string                          `json:"stopReason"`
	RawStopReason string                          `json:"rawStopReason,omitempty"`
	ErrorMessage  string                          `json:"errorMessage,omitempty"`
	Diagnostics   []ai.AssistantMessageDiagnostic `json:"diagnostics"`
}

// BugReportDiagnostics is diagnostics.json.
type BugReportDiagnostics struct {
	SchemaVersion         int                            `json:"schemaVersion"`
	SessionID             string                         `json:"sessionId"`
	EntryCount            int                            `json:"entryCount"`
	AssistantMessageCount int                            `json:"assistantMessageCount"`
	Assistant             []BugReportAssistantDiagnostic `json:"assistant"`
	Crashes               []CrashRecord                  `json:"crashes"`
}

// CollectBugReportDiagnostics collects failed assistant turns without
// conversation content, plus the crash log records without their notified
// flag. Mirrors upstream collectBugReportDiagnostics.
func CollectBugReportDiagnostics(sessionID string, entries []SessionEntry, crashes []CrashRecord) BugReportDiagnostics {
	diagnostics := BugReportDiagnostics{
		SchemaVersion: bugReportSchemaVersion,
		SessionID:     sessionID,
		EntryCount:    len(entries),
		Assistant:     []BugReportAssistantDiagnostic{},
		Crashes:       make([]CrashRecord, 0, len(crashes)),
	}
	for _, crash := range crashes {
		crash.Notified = false
		diagnostics.Crashes = append(diagnostics.Crashes, crash)
	}
	for _, entry := range entries {
		message, ok := entry.AsMessage()
		if !ok || message.Message.Assistant == nil {
			continue
		}
		diagnostics.AssistantMessageCount++
		assistant := message.Message.Assistant
		if len(assistant.Diagnostics) == 0 && assistant.StopReason != ai.StopReasonError && assistant.StopReason != ai.StopReasonAborted && assistant.ErrorMessage == "" {
			continue
		}
		turn := BugReportAssistantDiagnostic{
			EntryID:       entry.Base.ID,
			Timestamp:     entry.Base.Timestamp,
			Provider:      assistant.Provider,
			Model:         assistant.ModelID,
			API:           string(assistant.API),
			StopReason:    string(assistant.StopReason),
			RawStopReason: assistant.RawStopReason,
			ErrorMessage:  assistant.ErrorMessage,
			Diagnostics:   assistant.Diagnostics,
		}
		if turn.Diagnostics == nil {
			turn.Diagnostics = []ai.AssistantMessageDiagnostic{}
		}
		diagnostics.Assistant = append(diagnostics.Assistant, turn)
	}
	return diagnostics
}

// BugReportBundle is everything one report archive contains.
type BugReportBundle struct {
	Metadata     BugReportMetadata
	Diagnostics  BugReportDiagnostics
	SessionJSONL *string
	Summary      *string
}

// BugReportFile is one archive member.
type BugReportFile struct {
	Name string
	Data []byte
}

func bugReportJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// BugReportFiles lists the archive members in upstream order.
func BugReportFiles(bundle BugReportBundle) ([]BugReportFile, error) {
	report, err := bugReportJSON(bundle.Metadata)
	if err != nil {
		return nil, err
	}
	diagnostics, err := bugReportJSON(bundle.Diagnostics)
	if err != nil {
		return nil, err
	}
	files := []BugReportFile{{Name: "report.json", Data: report}, {Name: "diagnostics.json", Data: diagnostics}}
	if bundle.SessionJSONL != nil {
		files = append(files, BugReportFile{Name: "session.jsonl", Data: []byte(*bundle.SessionJSONL)})
	}
	if bundle.Summary != nil {
		summary := *bundle.Summary
		if !strings.HasSuffix(summary, "\n") {
			summary += "\n"
		}
		files = append(files, BugReportFile{Name: "summary.md", Data: []byte(summary)})
	}
	return files, nil
}

// WriteBugReportArchive writes the bundle as a deflated zip at path. The file
// is created exclusively so an existing file is never overwritten.
func WriteBugReportArchive(bundle BugReportBundle, path string) (err error) {
	files, err := BugReportFiles(bundle)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	archive := zip.NewWriter(file)
	modified := time.Now()
	for _, member := range files {
		writer, createErr := archive.CreateHeader(&zip.FileHeader{Name: member.Name, Method: zip.Deflate, Modified: modified})
		if createErr != nil {
			return createErr
		}
		if _, writeErr := writer.Write(member.Data); writeErr != nil {
			return writeErr
		}
	}
	return archive.Close()
}

// BugReportArchiveFileName names a report archive. Mirrors upstream
// bugReportArchiveFileName with PiG's command name (D2).
func BugReportArchiveFileName(id string) string {
	return AppName + "-bug-report-" + id + ".zip"
}

// BugReportIssueLink returns a prefilled PiG bug-form URL. It carries only the
// form's own fields: a title from the first line of the description (bounded),
// the PiG and Pi versions, the platform, and the archive name to attach. It
// never carries session content, settings, or paths.
func BugReportIssueLink(metadata BugReportMetadata, archiveName, upstreamVersion string) string {
	title := "bug: "
	if metadata.Hint != nil {
		firstLine, _, _ := strings.Cut(*metadata.Hint, "\n")
		title += truncateRunes(strings.TrimSpace(firstLine), bugReportIssueTitleRunes)
	}
	platform := metadata.Environment.Platform + "/" + metadata.Environment.Arch
	if terminal := metadata.Environment.Terminal.Program; terminal != nil {
		platform += ", " + *terminal
	} else if term := metadata.Environment.Terminal.Term; term != nil {
		platform += ", " + *term
	}
	fields := []struct{ key, value string }{
		{"template", bugReportIssueTemplate},
		{"title", title},
		{"version", fmt.Sprintf("%s %s (Pi %s)", AppName, metadata.Environment.Version, upstreamVersion)},
		{"platform", platform},
		{"actual", fmt.Sprintf("Report ID %s. The report archive %s is attached.", metadata.ID, archiveName)},
	}
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = field.key + "=" + url.QueryEscape(field.value)
	}
	return BugReportIssueURL + "?" + strings.Join(parts, "&")
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}

// bugReportArchivePath returns the archive path in dir for the report id.
func bugReportArchivePath(dir, id string) string {
	return filepath.Join(dir, BugReportArchiveFileName(id))
}

// BugReportBranch returns the entries on the current branch.
func BugReportBranch(session *Session) []SessionEntry {
	if session == nil {
		return nil
	}
	leaf := session.LeafID()
	if leaf == nil {
		return nil
	}
	return session.Branch(*leaf)
}
