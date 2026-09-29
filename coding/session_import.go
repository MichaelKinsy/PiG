// Ports importFromJsonl from packages/coding-agent/src/core/agent-session-runtime.ts.
package coding

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// ImportFromJsonl copies the session file at inputPath into the session
// directory and switches to the copy, as upstream importFromJsonl
// (agent-session-runtime.ts:361-404) does. inputPath resolves against the
// process working directory. A stored session is never replaced: a source
// that already lives in the session directory is opened in place, and any
// other source keeps its base name with the first free -N suffix and is
// copied exclusively. session_before_switch can cancel before anything is
// copied. cwdOverride replaces a stored working directory that no longer
// exists; without it such a session fails with MissingSessionCwdError after
// the copy.
func (s *Session) ImportFromJsonl(ctx context.Context, inputPath, cwdOverride string) (extension.CancelledResult, error) {
	resolvedPath, err := icodingagent.ResolvePath(inputPath, "")
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if _, err := os.Stat(resolvedPath); err != nil {
		return extension.CancelledResult{}, &icodingagent.SessionImportFileNotFoundError{FilePath: resolvedPath}
	}
	manager := newSessionManagerForDir(s.services, s.sessionDir)
	sessionDir := manager.SessionDir()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return extension.CancelledResult{}, err
	}
	destinationPath, sourceAlreadyStored := importDestination(sessionDir, resolvedPath)
	cancelled, err := s.beforeExtensionReplacement(ctx, extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "resume", TargetSessionFile: destinationPath})
	if err != nil || cancelled {
		return extension.CancelledResult{Cancelled: cancelled}, err
	}
	if !sourceAlreadyStored {
		if err := copyFileExclusive(resolvedPath, destinationPath); err != nil {
			return extension.CancelledResult{}, err
		}
	}
	var override []string
	if cwdOverride != "" {
		override = append(override, cwdOverride)
	}
	next, err := manager.Open(destinationPath, override...)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if err := icodingagent.AssertSessionCwdExists(next, s.services.CWD()); err != nil {
		return extension.CancelledResult{}, err
	}
	return s.finishExtensionReplacement(ctx, next, "resume")
}

// importDestination returns where importFromJsonl stores resolvedPath
// (agent-session-runtime.ts:372-380): the source itself when it already lives
// in sessionDir, otherwise its base name, then name-1, name-2, and so on
// until the name is free.
func importDestination(sessionDir, resolvedPath string) (destinationPath string, sourceAlreadyStored bool) {
	destinationPath = filepath.Join(sessionDir, filepath.Base(resolvedPath))
	if resolved, err := nodepath.Resolve(destinationPath); err == nil && resolved == resolvedPath {
		return destinationPath, true
	}
	base := filepath.Base(destinationPath)
	ext := filepath.Ext(base)
	if ext == base {
		// Node's path.parse keeps a lone leading dot in the name.
		ext = ""
	}
	name := strings.TrimSuffix(base, ext)
	for suffix := 1; pathExists(destinationPath); suffix++ {
		destinationPath = filepath.Join(sessionDir, name+"-"+strconv.Itoa(suffix)+ext)
	}
	return destinationPath, false
}

// pathExists reports what Node's existsSync reports: whether path names
// something that can be stat'ed.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// copyFileExclusive copies src to dst as Node's copyFileSync with
// COPYFILE_EXCL does: dst must not exist, it gets src's permission bits, and
// a failed copy removes the file it created.
func copyFileExclusive(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err = errors.Join(err, out.Close()); err != nil {
		return errors.Join(err, os.Remove(dst))
	}
	return nil
}
