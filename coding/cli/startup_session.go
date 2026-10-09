package cli

// Ports packages/coding-agent/src/main.ts.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

type startupSessionSelection struct {
	runtimeCWD   string
	sessionDir   string
	resumePath   string
	forkPath     string
	missingCWD   *missingSessionCWD
	crossProject *crossProjectSession
	manager      *coding.SessionManager
}

// applyName writes startup metadata before model validation. New Sessions carry the name to the mode that constructs them.
func (s startupSessionSelection) applyName(name string) (bool, error) {
	path := s.resumePath
	if s.forkPath != "" {
		path = s.forkPath
	}
	if name == "" || path == "" {
		return false, nil
	}
	// A --session path that does not exist yet selects a new Session.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	session, err := newSessionManagerWithDir(s.runtimeCWD, s.sessionDir).Load(path)
	if err != nil {
		return false, err
	}
	_, err = session.AppendSessionInfo(name)
	return err == nil, err
}

var listStartupSessions = func(manager *codingagent.SessionManager) ([]codingagent.SessionInfo, error) {
	return manager.ListCurrentSessions()
}

func (s startupSessionSelection) startOptions(flags Args) coding.SessionStartOptions {
	path := s.resumePath
	if s.forkPath != "" {
		path = s.forkPath
	}
	return coding.SessionStartOptions{
		SessionManager: s.manager,
		ResumePath:     path, SessionDir: s.sessionDir, SessionID: flags.SessionID,
		NoSession:   flags.NoSession || flags.Help || flags.ListModels != "" || flags.ListModelsAll,
		CWDOverride: flags.sessionCwdOverride,
	}
}

// loadSession retains the selected log for both model resolution and runtime construction, matching main.ts's shared SessionManager passed to createAgentSession.
func (s *startupSessionSelection) loadSession(flags Args) error {
	if s.manager != nil {
		return nil
	}
	path := s.resumePath
	if s.forkPath != "" {
		path = s.forkPath
	}
	if path == "" {
		return nil
	}
	var override []string
	if flags.sessionCwdOverride != nil {
		override = []string{*flags.sessionCwdOverride}
	}
	manager, err := newSessionManagerWithDir(s.runtimeCWD, s.sessionDir).Open(path, override...)
	if err != nil {
		return err
	}
	s.manager = manager
	return nil
}

type sessionAlreadyExistsError struct{ id string }

func (e *sessionAlreadyExistsError) Error() string {
	return fmt.Sprintf("Session already exists with id '%s'", e.id)
}

func formatSessionCreationWarning(id string, color bool) string {
	message := fmt.Sprintf("Warning: No project session found with id '%s'; creating a new session with that id.", id)
	if color {
		return "\x1b[33m" + message + "\x1b[39m"
	}
	return message
}

type crossProjectSession struct {
	path string
	cwd  string
}

type missingSessionCWD struct {
	sessionFile string
	storedCWD   string
	fallbackCWD string
}

func resolveStartupSessionSelection(flags Args, launchCWD, sessionDir string) (startupSessionSelection, error) {
	launchCWD = canonicalStartupDir(launchCWD)
	selection := startupSessionSelection{runtimeCWD: launchCWD, sessionDir: sessionDir}
	if flags.NoSession || flags.Help || flags.ListModels != "" || flags.ListModelsAll || flags.Resume {
		return selection, nil
	}

	manager := newSessionManagerWithDir(launchCWD, sessionDir)
	switch {
	case flags.Session != "":
		// Pi main.ts resolveSessionPath: a path argument opens that file, and
		// SessionManager.open starts a new Session there when it is missing.
		if path, ok, err := sessionPathArgument(launchCWD, flags.Session); ok {
			if err != nil {
				return selection, err
			}
			selection.resumePath = path
			break
		}
		resolved, err := resolveSessionArgument(manager, launchCWD, flags.Session)
		if err != nil {
			return selection, err
		}
		if resolved.global {
			selection.crossProject = &crossProjectSession{path: resolved.path, cwd: resolved.cwd}
			return selection, nil
		}
		selection.resumePath = resolved.path
	case flags.Fork != "":
		if flags.SessionID != "" && manager.FindByID(flags.SessionID) != "" {
			return selection, &sessionAlreadyExistsError{id: flags.SessionID}
		}
		// Pi main.ts createSessionManager forks a path argument whether or not
		// the file exists; forkFrom reports a missing or invalid source.
		source, ok, err := sessionPathArgument(launchCWD, flags.Fork)
		if !ok {
			var resolved resolvedSessionArgument
			resolved, err = resolveSessionArgument(manager, launchCWD, flags.Fork)
			source = resolved.path
		}
		if err != nil {
			return selection, err
		}
		var idOption []string
		if flags.SessionID != "" {
			idOption = []string{flags.SessionID}
		}
		// Pi forkSessionOrExit prints the forkFrom error as "Error: <message>".
		forked, err := manager.ForkFromFile(source, idOption...)
		if err != nil {
			return selection, err
		}
		selection.forkPath = forked.Path()
	case flags.Continue:
		selection.resumePath = manager.FindMostRecentForContinue()
	case flags.SessionID != "":
		selection.resumePath = manager.FindByID(flags.SessionID)
		if selection.resumePath == "" {
			color := chalkColorLevel(environMap(os.Environ()), os.Args[1:], term.IsTerminal(int(os.Stdout.Fd()))) > 0
			fmt.Fprintln(os.Stderr, formatSessionCreationWarning(flags.SessionID, color))
		}
	}

	path := selection.resumePath
	if selection.forkPath != "" {
		path = selection.forkPath
	}
	if path == "" {
		return selection, nil
	}
	if selection.sessionDir == "" {
		selection.sessionDir = filepath.Dir(path)
	}
	// Pi SessionManager.open reads the cwd only from an existing file's
	// header; a new explicit session path keeps the launch cwd.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return selection, nil
	}

	sessionCWD, err := readSessionCWD(path)
	if err != nil {
		return selection, err
	}
	if strings.TrimSpace(sessionCWD) == "" {
		return selection, nil
	}
	if !filepath.IsAbs(sessionCWD) {
		sessionCWD = filepath.Join(launchCWD, sessionCWD)
	}
	sessionCWD = canonicalStartupDir(sessionCWD)
	if _, statErr := os.Stat(sessionCWD); statErr != nil {
		selection.missingCWD = &missingSessionCWD{
			sessionFile: path,
			storedCWD:   sessionCWD,
			fallbackCWD: launchCWD,
		}
		return selection, nil
	}
	selection.runtimeCWD = sessionCWD
	return selection, nil
}

type resolvedSessionArgument struct {
	path   string
	cwd    string
	global bool
}

func canonicalStartupDir(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func confirmCrossProjectSession(reader io.Reader, writer io.Writer, cwd string) (bool, error) {
	if _, err := fmt.Fprintf(writer, "Session found in different project: %s\nFork this session into current directory? [y/N] ", cwd); err != nil {
		return false, err
	}
	answer, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func (issue missingSessionCWD) prompt() string {
	return codingagent.FormatMissingSessionCwdPrompt(codingagent.SessionCwdIssue{SessionFile: issue.sessionFile, SessionCwd: issue.storedCWD, FallbackCwd: issue.fallbackCWD})
}

func (issue missingSessionCWD) Error() string {
	return codingagent.FormatMissingSessionCwdError(codingagent.SessionCwdIssue{SessionFile: issue.sessionFile, SessionCwd: issue.storedCWD, FallbackCwd: issue.fallbackCWD})
}

func readSessionCWD(path string) (string, error) {
	header, err := codingagent.ReadSessionHeader(path)
	if err != nil {
		if _, ok := errors.AsType[*codingagent.SessionHeaderScanLimitError](err); !ok {
			return "", err
		}
		entries, loadErr := codingagent.LoadEntriesFromFile(path)
		if loadErr != nil {
			return "", loadErr
		}
		if len(entries) > 0 {
			// LoadEntriesFromFile returns records only when the first is a header.
			if first, ok := entries[0].(codingagent.SessionHeader); ok {
				header = &first
			}
		}
	}
	if header != nil {
		return header.CWD, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		session, err := codingagent.NewSessionManagerWithDir(cwd, filepath.Dir(path)).Open(path)
		if err != nil {
			return "", err
		}
		return session.CWD(), nil
	}
	return "", fmt.Errorf("Session file is not a valid pi session: %s", path)
}

// sessionNotFoundError is the shared unmatched ID/prefix result for --session and --fork. Unlike other startup failures, Pi prints it without an Error: prefix.
type sessionNotFoundError struct{ arg string }

func (err *sessionNotFoundError) Error() string {
	return fmt.Sprintf("No session found matching '%s'", err.arg)
}

// sessionPathURLError is the error Node's fileURLToPath throws from Pi's
// resolveSessionPath. Pi's main does not catch it, so Node reports it as an
// uncaught error headed by its name, code and message.
type sessionPathURLError struct{ err *nodeurl.Error }

func (err *sessionPathURLError) Error() string {
	if err.err.Code == "" {
		return "URIError: " + err.err.Message
	}
	return fmt.Sprintf("TypeError [%s]: %s", err.err.Code, err.err.Message)
}

func formatStartupSessionError(err error, color bool) string {
	if urlErr, ok := errors.AsType[*sessionPathURLError](err); ok {
		return urlErr.Error()
	}
	_, missing := errors.AsType[*sessionNotFoundError](err)
	_, exists := errors.AsType[*sessionAlreadyExistsError](err)
	if missing || exists {
		message := err.Error()
		if color {
			message = "\x1b[31m" + message + "\x1b[39m"
		}
		return message
	}
	return fmt.Sprintf("Error: %v", err)
}

// sessionPathArgument reports whether arg names a session file rather than a
// session ID, and returns that file's path resolved against launchCWD as Pi's
// resolvePath does. The file need not exist (Pi main.ts resolveSessionPath).
func sessionPathArgument(launchCWD, arg string) (string, bool, error) {
	if !strings.ContainsAny(arg, `/\\`) && !strings.HasSuffix(arg, ".jsonl") {
		return "", false, nil
	}
	path, err := codingagent.ResolvePath(arg, launchCWD)
	if urlErr, ok := errors.AsType[*nodeurl.Error](err); ok {
		return "", true, &sessionPathURLError{err: urlErr}
	}
	return path, true, err
}

func resolveSessionArgument(manager *codingagent.SessionManager, launchCWD, arg string) (resolvedSessionArgument, error) {
	if path, ok, err := sessionPathArgument(launchCWD, arg); ok {
		if err != nil {
			return resolvedSessionArgument{}, err
		}
		if _, err := os.Stat(path); err == nil {
			return resolvedSessionArgument{path: path}, nil
		}
		return resolvedSessionArgument{}, nil
	}
	resolved, err := findSession(func() ([]codingagent.SessionInfo, error) { return listStartupSessions(manager) }, arg)
	if err != nil || resolved.path != "" {
		return resolved, err
	}
	resolved, err = findSession(func() ([]codingagent.SessionInfo, error) { return manager.ListAllSessions() }, arg)
	if err != nil {
		return resolvedSessionArgument{}, err
	}
	if resolved.path == "" {
		return resolvedSessionArgument{}, &sessionNotFoundError{arg: arg}
	}
	resolved.global = true
	return resolved, nil
}

func findSession(loader func() ([]codingagent.SessionInfo, error), id string) (resolvedSessionArgument, error) {
	infos, err := loader()
	if err != nil {
		return resolvedSessionArgument{}, err
	}
	for _, info := range infos {
		if info.ID == id {
			return resolvedSessionArgument{path: info.Path, cwd: info.CWD}, nil
		}
	}
	for _, info := range infos {
		if strings.HasPrefix(info.ID, id) {
			return resolvedSessionArgument{path: info.Path, cwd: info.CWD}, nil
		}
	}
	return resolvedSessionArgument{}, nil
}
