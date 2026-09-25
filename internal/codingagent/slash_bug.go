package codingagent

// slash_bug.go ports upstream modes/interactive/bug-report.ts: consent, an
// optional model-written summary, then a local zip archive. PiG is
// export-only and links to the PiG issue form instead of uploading (D62).

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	bugReportDisclaimer     = "PiG writes this report as a zip archive in the current directory and never uploads it. It includes your pig version, operating system, the current model and provider configuration (without API keys), loaded extensions, settings, and provider error diagnostics from this session. You decide whether to attach it to a PiG issue on GitHub."
	bugReportTranscriptNote = "The transcript contains your messages, model output, tool calls and their results, including file contents and command output read during this session."
	bugReportCancelled      = "Bug report cancelled"
	bugReportExport         = "Export as Zip"
)

var errNoBugReportSummarizer = errors.New("the current session cannot write a summary")

// BugReportSessionEntryData is the custom entry /bug records in the session.
// Mirrors upstream BugReportSessionEntryData; delivery is always "zip".
type BugReportSessionEntryData struct {
	ID              string  `json:"id"`
	CreatedAt       string  `json:"createdAt"`
	Hint            *string `json:"hint"`
	SessionIncluded bool    `json:"sessionIncluded"`
	SummaryIncluded bool    `json:"summaryIncluded"`
	Delivery        string  `json:"delivery"`
	Path            string  `json:"path,omitempty"`
}

type bugReportChoices struct {
	hint           string
	includeSession bool
	includeSummary bool
}

// bugHandler runs /bug. The optional argument prefills the description.
func bugHandler(sc *SlashContext) error {
	if sc.ShowExtensionEditor == nil || sc.ShowExtensionSelector == nil || sc.BugReportInputs == nil {
		sc.Append("/bug needs the interactive UI.")
		return nil
	}
	choices, ok := promptBugReportChoices(sc)
	if !ok {
		showStatusOrAppend(sc, bugReportCancelled)
		return nil
	}

	var summary *string
	if choices.includeSummary {
		if sc.SummarizeForBugReport == nil {
			return errors.New("Failed to write bug report summary: no model is available")
		}
		text, aborted, err := sc.SummarizeForBugReport(bugReportModelName(sc), choices.hint)
		if aborted {
			showStatusOrAppend(sc, bugReportCancelled)
			return nil
		}
		if err != nil {
			return fmt.Errorf("Failed to write bug report summary: %w", err)
		}
		summary = &text
	}

	bundle, err := buildBugReportBundle(sc, choices, summary)
	if err != nil {
		return fmt.Errorf("Failed to build bug report: %w", err)
	}
	return exportBugReport(sc, bundle)
}

func bugReportModelName(sc *SlashContext) string {
	if sc.ModelName != nil {
		if name := sc.ModelName(); name != "" {
			return name
		}
	}
	return "the current model"
}

// promptBugReportChoices asks for the description, transcript consent, and
// summary consent, then confirms the export. It mirrors upstream
// promptForOptions without the upload choice.
func promptBugReportChoices(sc *SlashContext) (bugReportChoices, bool) {
	hint, ok := sc.ShowExtensionEditor("Report a bug", bugReportDisclaimer+"\n\nWhat went wrong? (optional)", strings.TrimSpace(sc.Args))
	if !ok {
		return bugReportChoices{}, false
	}
	transcript, ok := sc.ShowExtensionSelector("Include the session transcript?", []string{"Yes, include the transcript", "No"}, bugReportTranscriptNote)
	if !ok {
		return bugReportChoices{}, false
	}
	choices := bugReportChoices{hint: strings.TrimSpace(hint), includeSession: transcript != "No"}
	modelName := bugReportModelName(sc)
	if !choices.includeSession {
		provider := "your provider"
		if sc.BugReportProviderName != nil {
			if name := sc.BugReportProviderName(); name != "" {
				provider = name
			}
		}
		summary, ok := sc.ShowExtensionSelector(
			"Attach a summary written by "+modelName+" instead?",
			[]string{"Yes, generate a summary", "No"},
			"The transcript is sent to "+provider+" with your credentials and tokens. Only the generated summary is attached; the transcript stays on your machine.",
		)
		if !ok {
			return bugReportChoices{}, false
		}
		choices.includeSummary = summary != "No"
	}
	description := choices.hint
	if description == "" {
		description = "none"
	}
	transcriptState := "not included"
	if choices.includeSession {
		transcriptState = "included"
	}
	summaryState := "none"
	if choices.includeSummary {
		summaryState = "written by " + modelName
	}
	// pig divergence (D62): export is the only delivery; PiG never uploads.
	delivery, ok := sc.ShowExtensionSelector("Bug report", []string{bugReportExport, "Cancel"}, fmt.Sprintf(
		"Description: %s\nTranscript: %s\nSummary: %s\n\nExport writes a zip archive to the current directory. Nothing is uploaded; you can attach the archive to a PiG issue.",
		description, transcriptState, summaryState,
	))
	if !ok || delivery != bugReportExport {
		return bugReportChoices{}, false
	}
	return choices, true
}

func buildBugReportBundle(sc *SlashContext, choices bugReportChoices, summary *string) (BugReportBundle, error) {
	inputs, err := sc.BugReportInputs()
	if err != nil {
		return BugReportBundle{}, err
	}
	var session *Session
	if sc.CurrentSession != nil {
		session = sc.CurrentSession()
	}
	if session != nil {
		inputs.SessionID = session.ID()
		inputs.CWD = session.CWD()
		inputs.Header = session.Header()
		inputs.Entries = session.Entries()
		inputs.Branch = BugReportBranch(session)
	}
	now := time.Now()
	metadata, err := CollectBugReportMetadata(inputs, BugReportOptions{Hint: choices.hint, IncludeSession: choices.includeSession}, summary != nil, now)
	if err != nil {
		return BugReportBundle{}, err
	}
	bundle := BugReportBundle{
		Metadata:    metadata,
		Diagnostics: CollectBugReportDiagnostics(inputs.SessionID, inputs.Entries, ReadCrashLog(bugReportCrashLogPath(sc))),
		Summary:     summary,
	}
	if choices.includeSession {
		// Upstream attaches the pi.share presentation entry to the transcript.
		var trailing TrailingEntries
		if sc.ShareState != nil {
			trailing = shareTrailingEntries(sc.ShareState())
		}
		jsonl, err := SerializeSessionBranch(inputs.Header, inputs.Branch, now, trailing)
		if err != nil {
			return BugReportBundle{}, err
		}
		bundle.SessionJSONL = &jsonl
	}
	return bundle, nil
}

// exportBugReport writes the archive to the working directory, records the
// report in the session, and prints the PiG issue link.
func exportBugReport(sc *SlashContext, bundle BugReportBundle) error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("Failed to write bug report: %w", err)
	}
	archiveName := BugReportArchiveFileName(bundle.Metadata.ID)
	archivePath := bugReportArchivePath(dir, bundle.Metadata.ID)
	if err := WriteBugReportArchive(bundle, archivePath); err != nil {
		return fmt.Errorf("Failed to write bug report: %w", err)
	}
	if err := recordBugReport(sc, bundle, archivePath); err != nil {
		return fmt.Errorf("Bug report exported to %s, but recording it in the session failed: %w", archivePath, err)
	}
	// Upstream recordInSession clears the crash log once a report carries it.
	if len(bundle.Diagnostics.Crashes) > 0 {
		ClearCrashLog(bugReportCrashLogPath(sc))
	}
	showStatusOrAppend(sc, fmt.Sprintf("Bug report exported to: %s\nReport ID: %s", archivePath, bundle.Metadata.ID))
	upstreamVersion := ""
	if sc.UpstreamVersion != nil {
		upstreamVersion = sc.UpstreamVersion()
	}
	// pig divergence (D62): link to the PiG issue form; the user attaches the archive.
	sc.Append(fmt.Sprintf("To report it, open a PiG issue and attach `%s`:\n\n%s", archiveName, BugReportIssueLink(bundle.Metadata, archiveName, upstreamVersion)))
	return nil
}

func recordBugReport(sc *SlashContext, bundle BugReportBundle, archivePath string) error {
	if sc.CurrentSession == nil {
		return nil
	}
	session := sc.CurrentSession()
	if session == nil {
		return nil
	}
	id, err := generateEntryID()
	if err != nil {
		return err
	}
	return session.AppendEntry(CustomEntry{
		SessionEntryBase: SessionEntryBase{Type: "custom", ID: id, ParentID: session.LeafID(), Timestamp: isoTimestamp(time.Now())},
		CustomType:       BugReportCustomEntryType,
		Data: BugReportSessionEntryData{
			ID:              bundle.Metadata.ID,
			CreatedAt:       bundle.Metadata.CreatedAt,
			Hint:            bundle.Metadata.Hint,
			SessionIncluded: bundle.Metadata.Session.Included,
			SummaryIncluded: bundle.Metadata.Session.SummaryIncluded,
			Delivery:        "zip",
			Path:            archivePath,
		},
	})
}

// bugReportCrashLogPath locates the crash log /bug attaches, or "" (no log)
// when the context has no agent directory.
func bugReportCrashLogPath(sc *SlashContext) string {
	if sc.AgentDir == "" {
		return ""
	}
	return CrashLogPath(sc.AgentDir)
}
