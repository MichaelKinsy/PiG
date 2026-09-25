package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type startupSessionSelection struct {
	runtimeCWD   string
	sessionDir   string
	resumePath   string
	forkPath     string
	missingCWD   *missingSessionCWD
	crossProject *crossProjectSession
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

func resolveStartupSessionSelection(flags CLIFlags, launchCWD, sessionDir string) (startupSessionSelection, error) {
	launchCWD = canonicalStartupDir(launchCWD)
	selection := startupSessionSelection{runtimeCWD: launchCWD, sessionDir: sessionDir}
	if flags.NoSession || flags.ListModels != "" || flags.ListModelsAll || flags.ResumeAny {
		return selection, nil
	}

	manager := newSessionManagerWithDir(launchCWD, sessionDir)
	switch {
	case flags.Session != "":
		resolved, err := resolveSessionArgument(manager, launchCWD, flags.Session)
		if err != nil {
			return selection, err
		}
		if resolved.path == "" {
			return selection, fmt.Errorf("session %q not found in %s", flags.Session, manager.SessionDir())
		}
		if resolved.global {
			selection.crossProject = &crossProjectSession{path: resolved.path, cwd: resolved.cwd}
			return selection, nil
		}
		selection.resumePath = resolved.path
	case flags.Fork != "":
		resolved, err := resolveSessionArgument(manager, launchCWD, flags.Fork)
		if err != nil {
			return selection, err
		}
		if resolved.path == "" {
			return selection, fmt.Errorf("session %q not found for fork in %s", flags.Fork, manager.SessionDir())
		}
		forked, err := manager.ForkFromFile(resolved.path)
		if err != nil {
			return selection, fmt.Errorf("fork session: %w", err)
		}
		selection.forkPath = forked.Path()
	case flags.Continue:
		selection.resumePath = manager.FindMostRecentForContinue()
	case flags.SessionID != "":
		resolved, err := findExactSession(manager.ListCurrentSessions, flags.SessionID)
		if err != nil {
			return selection, err
		}
		selection.resumePath = resolved.path
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
	if info, statErr := os.Stat(sessionCWD); statErr != nil || !info.IsDir() {
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
	return fmt.Sprintf("cwd from session file does not exist\n%s\n\ncontinue in current cwd\n%s", issue.storedCWD, issue.fallbackCWD)
}

func (issue missingSessionCWD) Error() string {
	return fmt.Sprintf("Stored session working directory does not exist: %s\nSession file: %s\nCurrent working directory: %s", issue.storedCWD, issue.sessionFile, issue.fallbackCWD)
}

func readSessionCWD(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("session: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Only the header line is read; it has no length limit, as upstream
	// reads session files whole.
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if len(line) == 0 {
		return "", fmt.Errorf("session: empty file: %s", path)
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})

	var header struct {
		Type string `json:"type"`
		CWD  string `json:"cwd"`
	}
	if err := json.Unmarshal(line, &header); err != nil {
		return "", fmt.Errorf("session: parse header: %w", err)
	}
	if header.Type != "session" {
		return "", fmt.Errorf("session: missing header in %s", path)
	}
	return header.CWD, nil
}

func resolveSessionArgument(manager *codingagent.SessionManager, launchCWD, arg string) (resolvedSessionArgument, error) {
	if strings.ContainsAny(arg, `/\\`) || strings.HasSuffix(arg, ".jsonl") {
		if filepath.IsAbs(arg) {
			if _, err := os.Stat(arg); err == nil {
				return resolvedSessionArgument{path: filepath.Clean(arg)}, nil
			}
			return resolvedSessionArgument{}, nil
		}
		path := filepath.Join(launchCWD, arg)
		if _, err := os.Stat(path); err == nil {
			return resolvedSessionArgument{path: filepath.Clean(path)}, nil
		}
		return resolvedSessionArgument{}, nil
	}
	resolved, err := findSession(manager.ListCurrentSessions, arg)
	if err != nil || resolved.path != "" {
		return resolved, err
	}
	resolved, err = findSession(manager.ListAllSessions, arg)
	if err != nil {
		return resolvedSessionArgument{}, err
	}
	resolved.global = resolved.path != ""
	return resolved, nil
}

func findExactSession(loader func() ([]codingagent.SessionInfo, error), id string) (resolvedSessionArgument, error) {
	infos, err := loader()
	if err != nil {
		return resolvedSessionArgument{}, err
	}
	for _, info := range infos {
		if info.ID == id {
			return resolvedSessionArgument{path: info.Path, cwd: info.CWD}, nil
		}
	}
	return resolvedSessionArgument{}, nil
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
