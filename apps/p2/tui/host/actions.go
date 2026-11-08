package host

// The launcher's actions (p2-tui-port.md sections 3.1-3.5, P5): clipboard, browser, downloads, external editor, job control, the dev channel and sign-out.
// Every one runs under a grant: the visitor's own privileged key (K) or a confirmation in the launcher-drawn consent screen of exactly the data used (C).
// Worker payloads are validated here; URLs are built from the launcher's fixed origin; credentials never enter a URL, a child process or the worker.
//
// pi: apps/p2/src/hosts/tui-actions.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Limits of the actions.
const (
	MaxCopy     = 1024 * 1024
	MaxDownload = 25 * 1024 * 1024
	MaxUpload   = 25 * 1024 * 1024
	maxEditor   = 1024 * 1024
)

var (
	uuidSyntax     = lazyregexp.New(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	artifactName   = lazyregexp.New(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	sessionPage    = lazyregexp.New(`(?i)^/s/[0-9a-f-]{36}$`)
	versionNumber  = lazyregexp.New(`^\d{1,6}$`)
	quoted         = lazyregexp.New(`^(['"]).*(['"])$`)
	backslashPair  = lazyregexp.New(`\\(.)`)
	controlsAndSep = lazyregexp.New("[\\x00-\\x1f\\x7f-\\x9f/:*?\"<>|]")
	leadingDots    = lazyregexp.New(`^[.\s]+`)
	pastedControls = lazyregexp.New(`[\x00-\x08\x0b-\x1f\x7f-\x9f]`)
	editorWord     = lazyregexp.New(`"([^"]*)"|'([^']*)'|(\S+)`)
	filenameStar   = lazyregexp.New(`(?i)filename\*=UTF-8''([^;]+)`)
	filenamePlain  = lazyregexp.New(`(?i)filename="([^"]*)"`)
)

// UploadedFile is the answer of the server's upload route.
type UploadedFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType,omitempty"`
}

// LauncherActionOptions configure LauncherActions.
type LauncherActionOptions struct {
	URL         *url.URL
	Credentials Credentials
	// Transport is how requests reach the server; default: directly to URL with Credentials.
	Transport Transport
	// Role is "owner" or "guest".
	Role string
	// Downloads is the download folder (launcher-only preference; default ~/Downloads/p2).
	Downloads string
	// Editor is the external editor argv (launcher-only: pi's `externalEditor`, else $VISUAL/$EDITOR); never sent to the worker.
	Editor []string
	// Clipboard, OpenBrowser and RunEditor are injectable for tests. RunEditor's ok is false when the editor could not run.
	Clipboard   func(text string, writeTerminal func(data string)) error
	OpenBrowser func(url string)
	RunEditor   func(argv []string, file string) (code int, ok bool)
	// ReadClipboard are the clipboard readers for Ctrl+V (injectable for tests); nil fields use the platform's.
	ReadClipboard *ClipboardReaders
	// PickerStart is where the launcher's file picker starts (default: the working directory).
	PickerStart string
	// PrefsDirectory is the directory of p2's own `prefs.json` (default: `P2_TUI_CONFIG_DIR` or `~/.pi/p2-tui`).
	PrefsDirectory string
	// SuspendProcess stops this process group (SIGTSTP) and returns on SIGCONT.
	SuspendProcess func() error
	Platform       string
	// OnSignedOut is called after a successful sign-out of this device's cookie (the launcher forgets it).
	OnSignedOut func(all bool)
}

// ClipboardReaders are the readers of Ctrl+V.
type ClipboardReaders struct {
	Files func() []string
	Image func() *ClipboardImage
	Text  func() string
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func count(n int) string {
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// reported is one line of worker-supplied text for a fact, marked as the server's.
func reported(value any) string {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return ""
	}
	return jsSlice(strings.TrimSpace(text), 120) + " (server-reported)"
}

func firstLine(text string) string {
	line := ""
	for _, candidate := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(candidate) != "" {
			line = candidate
			break
		}
	}
	if utf16Len(line) > 60 {
		return jsSlice(line, 60) + "…"
	}
	return line
}

func sizeText(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(bytes)/1048576)
}

var imageExtensions = map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}

// LocalPath is a local path as typed or dropped: quotes, `file://` URLs, backslash escapes and `~/` resolved on this computer.
func LocalPath(value string) (string, bool) {
	path := strings.TrimSpace(value)
	if path == "" || utf16Len(path) > 4096 || strings.Contains(path, "\x00") {
		return "", false
	}
	if m := quoted.FindStringSubmatch(path); m != nil && m[1] == m[2] && len(path) >= 2 {
		path = path[1 : len(path)-1]
	}
	if strings.HasPrefix(path, "file://") {
		u, err := url.Parse(path)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return "", false
		}
		path = u.Path
	} else {
		path = backslashPair.ReplaceAllString(path, "$1")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = filepath.Join(homeDir(), path[1:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return abs, true
}

// LocalFile is an opened local file: section 3.3 step 7.
type LocalFile struct {
	*os.File
	Size     int64
	Name     string
	MimeType string
}

// OpenLocalFile opens path without following a final symlink, checks the opened descriptor is a regular file within the limit; everything after reads
// through that descriptor.
func OpenLocalFile(path string) (*LocalFile, error) {
	file, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("Not a regular file")
	}
	if err == nil && info.Size() > MaxUpload {
		err = fmt.Errorf("Larger than 25 MiB (%s)", sizeText(info.Size()))
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	name := UploadName(filepath.Base(path))
	extension := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	return &LocalFile{File: file, Size: info.Size(), Name: name, MimeType: imageExtensions[extension]}, nil
}

// UploadName is the upload name: the local base name without controls or separators, at most 255 characters (section 3.4).
func UploadName(value string) string { return jsSlice(SafeFileName(value), 255) }

// EditorArgv splits an editor command into argv without a shell (quotes group words; nothing is expanded).
func EditorArgv(command string) []string {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	var argv []string
	for _, m := range editorWord.FindAllStringSubmatch(command, -1) {
		switch {
		case m[3] != "":
			argv = append(argv, m[3])
		case strings.HasPrefix(m[0], `"`):
			argv = append(argv, m[1])
		default:
			argv = append(argv, m[2])
		}
	}
	return argv
}

// SafeFileName is a local file name from a server-supplied one: base name only, no controls, separators or leading dots.
func SafeFileName(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	base := ""
	if trimmed := strings.TrimRight(value, "/"); trimmed != "" {
		base = trimmed[strings.LastIndex(trimmed, "/")+1:]
	}
	name := controlsAndSep.ReplaceAllString(base, "_")
	name = strings.TrimSpace(leadingDots.ReplaceAllString(name, ""))
	if name == "" {
		name = "download"
	}
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

// dispositionName is Content-Disposition's filename* (RFC 5987) or filename.
func dispositionName(header string) string {
	if header == "" {
		return ""
	}
	if m := filenameStar.FindStringSubmatch(header); m != nil {
		if decoded, err := url.PathUnescape(m[1]); err == nil {
			return decoded
		}
	}
	if m := filenamePlain.FindStringSubmatch(header); m != nil {
		return m[1]
	}
	return ""
}

// OriginPath is section 3.5: only these fixed-origin paths open in the browser.
func OriginPath(base *url.URL, path any) *url.URL {
	text, ok := path.(string)
	if !ok || utf16Len(text) > 512 || !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return nil
	}
	target, err := base.Parse(text)
	if err != nil || OriginOf(target) != OriginOf(base) || target.Fragment != "" || strings.Contains(text, "#") || target.User != nil {
		return nil
	}
	var keys []string
	if target.RawQuery != "" {
		for _, pair := range strings.Split(target.RawQuery, "&") {
			name, _, _ := strings.Cut(pair, "=")
			decoded, err := url.QueryUnescape(name)
			if err != nil {
				decoded = name
			}
			keys = append(keys, decoded)
		}
	}
	only := func(allowed ...string) bool {
		seen := map[string]bool{}
		for _, key := range keys {
			if !contains(allowed, key) || seen[key] {
				return false
			}
			seen[key] = true
		}
		return true
	}
	query := target.Query()
	session := query.Get("session")
	switch {
	case sessionPage.MatchString(target.Path) && uuidSyntax.MatchString(target.Path[3:]) && len(keys) == 0:
		return target
	case target.Path == "/artifact" && only("session", "name", "version", "download") && uuidSyntax.MatchString(session) && artifactName.MatchString(query.Get("name")) && versionNumber.MatchString(query.Get("version")):
		return target
	case target.Path == "/download" && only("session", "id") && uuidSyntax.MatchString(session) && uuidSyntax.MatchString(query.Get("id")):
		return target
	}
	return nil
}

// saveBounded saves a response body into folder under a fresh name (never overwrites), bounded and abortable.
func saveBounded(ctx context.Context, body io.Reader, folder, name string) (string, error) {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if stem == "" {
		stem = "download"
	}
	var file *os.File
	path := ""
	for index := 0; index < 1000 && file == nil; index++ {
		candidate := name
		if index > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, index, extension)
		}
		path = filepath.Join(folder, candidate)
		opened, err := createExclusive(path)
		if err == nil {
			file = opened
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	if file == nil {
		return "", errors.New("No free file name in the download folder")
	}
	fail := func(err error) (string, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	buffer := make([]byte, 32*1024)
	size := 0
	for {
		if ctx.Err() != nil {
			return fail(errors.New("Download cancelled"))
		}
		n, err := body.Read(buffer)
		size += n
		if size > MaxDownload {
			return fail(errors.New("Download exceeds 25 MiB"))
		}
		if n > 0 {
			if _, werr := file.Write(buffer[:n]); werr != nil {
				return fail(werr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
	}
	return path, file.Close()
}

func spawnEditor(argv []string, file string) (int, bool) {
	command := exec.Command(argv[0], append(append([]string{}, argv[1:]...), file)...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), true
		}
		return 0, false
	}
	return 0, true
}

type openTarget struct {
	url      *url.URL
	external bool
}

type downloadTarget struct {
	path     string
	label    any
	fallback string
}

// orderedQuery is URLSearchParams' serialization: pairs in the given order, escaped as a form.
func orderedQuery(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, url.QueryEscape(pairs[i])+"="+url.QueryEscape(pairs[i+1]))
	}
	return strings.Join(parts, "&")
}

// limitedReader fails once more than limit bytes were read.
type limitedReader struct {
	r    io.Reader
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n, errors.New("Larger than 25 MiB")
	}
	return n, err
}

// LauncherActions are the launcher's actions by name.
func LauncherActions(options LauncherActionOptions) map[string]LauncherAction {
	base := options.URL
	platform := options.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	transport := options.Transport
	if transport == nil {
		transport = CookieTransport(base, options.Credentials)
	}
	clipboard := options.Clipboard
	if clipboard == nil {
		clipboard = func(text string, write func(string)) error { return CopyToClipboard(text, write, nil, platform) }
	}
	open := options.OpenBrowser
	if open == nil {
		open = func(target string) { OpenBrowser(target, platform) }
	}
	folder := options.Downloads
	if folder == "" {
		folder = filepath.Join(homeDir(), "Downloads", "p2")
	}
	loopback := base.Scheme == "http" && isLoopbackHost(base)
	prefsDirectory := options.PrefsDirectory
	if prefsDirectory == "" {
		prefsDirectory = ConfigDirectory()
	}
	pickerStart := options.PickerStart
	if pickerStart == "" {
		pickerStart, _ = os.Getwd()
	}
	reader := ClipboardReaders{
		Files: ReadClipboardFilePaths,
		Image: ReadClipboardImage,
		Text:  func() string { return ReadClipboardText(nil, platform) },
	}
	if options.ReadClipboard != nil {
		if options.ReadClipboard.Files != nil {
			reader.Files = options.ReadClipboard.Files
		}
		if options.ReadClipboard.Image != nil {
			reader.Image = options.ReadClipboard.Image
		}
		if options.ReadClipboard.Text != nil {
			reader.Text = options.ReadClipboard.Text
		}
	}
	runEditor := options.RunEditor
	if runEditor == nil {
		runEditor = spawnEditor
	}
	suspend := options.SuspendProcess
	if suspend == nil {
		suspend = suspendThisProcess
	}
	request := func(ctx context.Context, path string, r Request) (*http.Response, error) {
		return transport.Request(ctx, path, r)
	}
	copyText := func(value any) (string, bool) {
		text, ok := record(value)["text"].(string)
		return text, ok && len(text) > 0 && len(text) <= MaxCopy
	}
	copied := func(text string, ctx ActionContext) error {
		if err := clipboard(text, ctx.WriteTerminal); err != nil {
			return err
		}
		ctx.UI.Notice(fmt.Sprintf("Copied %s characters: %s", count(utf16Len(text)), firstLine(text)), 1200*time.Millisecond)
		return nil
	}

	// upload is section 3.4: fixed URL, exact Origin and the launcher's cookie, no redirects, 60 s, at most 25 MiB counted while streaming, a bounded
	// `{id}` answer. Errors stay in the launcher.
	upload := func(ctx context.Context, session, name string, body io.Reader, size int64) (UploadedFile, error) {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		response, err := request(ctx, "/upload?"+orderedQuery("session", session, "name", name), Request{Method: http.MethodPost, Headers: map[string]string{"Content-Type": "application/octet-stream"}, Body: body})
		if err != nil {
			return UploadedFile{}, err
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		var value map[string]any
		_ = json.Unmarshal(data, &value)
		if response.StatusCode < 200 || response.StatusCode > 299 {
			return UploadedFile{}, fmt.Errorf("The server refused the upload (%d)", response.StatusCode)
		}
		id, _ := value["id"].(string)
		if !uuidSyntax.MatchString(id) {
			return UploadedFile{}, errors.New("Invalid upload response")
		}
		file := UploadedFile{ID: id, Name: name, Size: size}
		if n, ok := value["name"].(string); ok {
			file.Name = jsSlice(n, 255)
		}
		if n, ok := value["size"].(float64); ok {
			file.Size = int64(n)
		}
		if m, ok := value["mimeType"].(string); ok && m != "" {
			file.MimeType = jsSlice(m, 100)
		}
		return file, nil
	}
	// attachLocal is a local file: facts first (path, size, type, session), the upload only after Enter or y.
	attachLocal := func(path string, ctx ActionContext) (*UploadedFile, error) {
		if ctx.Grant.Target == nil {
			return nil, errors.New("Open a session to attach files")
		}
		session := ctx.Grant.Target.Session
		file, err := OpenLocalFile(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		kind := file.MimeType
		if kind == "" {
			kind = "file"
		}
		confirmed := ctx.UI.Confirm(ConsentRequest{
			Action:       "Upload a local file to this session",
			Facts:        []ConsentFact{{"File", path}, {"Size", sizeText(file.Size)}, {"Type", kind}, {"Session", session}},
			ConfirmLabel: "upload",
		})
		if !confirmed {
			return nil, nil
		}
		uploaded, err := upload(ctx.Grant.Context, session, file.Name, &limitedReader{r: file, left: MaxUpload}, file.Size)
		if err != nil {
			return nil, err
		}
		return &uploaded, nil
	}
	attached := func(file *UploadedFile, ctx ActionContext) error {
		if file == nil {
			return nil
		}
		_, err := ctx.Ask("attached", file)
		return err
	}

	resolveOpen := func(value map[string]any, session string) *openTarget {
		if value["kind"] == "external" {
			text, ok := value["url"].(string)
			if !ok || utf16Len(text) > 2048 {
				return nil
			}
			target, err := url.Parse(text)
			if err != nil || target.Host == "" {
				return nil
			}
			if target.Scheme == "https" && target.User == nil {
				return &openTarget{target, true}
			}
			return nil
		}
		if value["kind"] != "origin" {
			return nil
		}
		target := OriginPath(base, value["path"])
		// Session content opens only for the session the worker is bound to; any session's page may open.
		content := target != nil && target.Path != "/" && !strings.HasPrefix(target.Path, "/s/")
		if target == nil || (content && target.Query().Get("session") != session) {
			return nil
		}
		return &openTarget{target, false}
	}
	resolveDownload := func(value map[string]any, session string) *downloadTarget {
		sid, _ := value["session"].(string)
		if value == nil || sid == "" || sid != session || !uuidSyntax.MatchString(sid) {
			return nil
		}
		if id, _ := value["id"].(string); value["kind"] == "file" && uuidSyntax.MatchString(id) {
			name, _ := value["name"].(string)
			return &downloadTarget{path: "/download?" + orderedQuery("session", sid, "id", id), label: value["name"], fallback: name}
		}
		name, _ := value["name"].(string)
		version, isNumber := value["version"].(float64)
		if value["kind"] == "artifact" && artifactName.MatchString(name) && isNumber && version == float64(int(version)) && version >= 0 && version < 1_000_000 {
			return &downloadTarget{path: "/artifact?" + orderedQuery("session", sid, "name", name, "version", strconv.Itoa(int(version)), "download", "1"), label: fmt.Sprintf("%s v%d", name, int(version)), fallback: name}
		}
		return nil
	}
	sessionOf := func(target *GrantTarget) string {
		if target == nil {
			return ""
		}
		return target.Session
	}

	actions := map[string]LauncherAction{}
	// C (`/attach [path]`): a dropped or typed path is shown with its facts before upload; without a path the launcher's own file picker chooses.
	// Payload `{path?}`; result `{id, name, size, mimeType?}`.
	actions["attach"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, ctx ConsentContext) *ConsentRequest {
			value, isObject := payload.(map[string]any)
			if !isObject || ctx.Target == nil {
				return nil
			}
			raw, hasPath := value["path"]
			text, isString := raw.(string)
			if hasPath && !isString {
				return nil
			}
			path := ""
			if isString {
				var ok bool
				if path, ok = LocalPath(text); !ok {
					return nil
				}
			}
			if path != "" {
				return &ConsentRequest{Action: "Show this local file for upload (the next screen has its size and type)", Facts: []ConsentFact{{"File", path}}, ConfirmLabel: "continue"}
			}
			return &ConsentRequest{Action: "Choose a local file to upload (in the launcher's file picker)", ConfirmLabel: "open the picker"}
		},
		Run: func(payload any, ctx ActionContext) (any, error) {
			path := ""
			if text, ok := record(payload)["path"].(string); ok {
				path, _ = LocalPath(text)
			} else {
				path, _ = ctx.UI.Pick("Choose a file to upload", pickerStart)
			}
			if path == "" {
				return nil, nil
			}
			file, err := attachLocal(path, ctx)
			if file == nil || err != nil {
				return nil, err
			}
			return *file, nil
		},
	}
	// K (p2.attach, unbound by default): the launcher's file picker, then the facts, then the upload.
	actions["attach.key"] = LauncherAction{Kind: GrantK, Key: "p2.attach", RunKey: func(ctx ActionContext) error {
		path, ok := ctx.UI.Pick("Choose a file to upload", pickerStart)
		if !ok {
			return nil
		}
		file, err := attachLocal(path, ctx)
		if err != nil {
			return err
		}
		return attached(file, ctx)
	}}
	// K (Ctrl+V, pi's app.clipboard.pasteImage): copied files (macOS) and images upload after a confirmation; plain text is pasted into the worker as
	// a bracketed paste (the visitor's own paste key).
	actions["paste.key"] = LauncherAction{Kind: GrantK, Key: "app.clipboard.pasteImage", RunKey: func(ctx ActionContext) error {
		if files := reader.Files(); len(files) > 0 {
			for _, path := range files[:min(10, len(files))] {
				abs, _ := filepath.Abs(path)
				file, err := attachLocal(abs, ctx)
				if err != nil {
					return err
				}
				if err := attached(file, ctx); err != nil {
					return err
				}
			}
			return nil
		}
		if image := reader.Image(); image != nil {
			if ctx.Grant.Target == nil {
				return errors.New("Open a session to attach images")
			}
			session := ctx.Grant.Target.Session
			extension := strings.TrimPrefix(image.MimeType, "image/")
			if extension == "jpeg" {
				extension = "jpg"
			}
			stamp := strings.NewReplacer(":", "-", ".", "-").Replace(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
			name := "clipboard-" + stamp + "." + extension
			if !ctx.UI.Confirm(ConsentRequest{
				Action:       "Upload the clipboard image to this session",
				Facts:        []ConsentFact{{"Image", fmt.Sprintf("%s, %s", image.MimeType, sizeText(int64(len(image.Bytes))))}, {"Name", name}, {"Session", session}},
				ConfirmLabel: "upload",
			}) {
				return nil
			}
			if len(image.Bytes) > MaxUpload {
				return errors.New("Larger than 25 MiB")
			}
			file, err := upload(ctx.Grant.Context, session, name, bytes.NewReader(image.Bytes), int64(len(image.Bytes)))
			if err != nil {
				return err
			}
			return attached(&file, ctx)
		}
		if clean := pastedControls.ReplaceAllString(reader.Text(), ""); clean != "" {
			ctx.SendInput("\x1b[200~" + clean + "\x1b[201~")
		}
		return nil
	}}
	// K (Ctrl+X): the worker supplies the selection or last answer; the launcher writes and says what it copied.
	actions["copy.key"] = LauncherAction{Kind: GrantK, Key: "app.message.copy", RunKey: func(ctx ActionContext) error {
		answer, err := ctx.Ask("copyText", nil)
		if err != nil {
			return err
		}
		text, ok := copyText(answer)
		if !ok {
			ctx.UI.Notice("Nothing to copy", 1200*time.Millisecond)
			return nil
		}
		return copied(text, ctx)
	}}
	// pi-tui's copy-on-select (pi's `fullscreenCopyOnSelect`): the worker's TuiAltScreen hands the selected text to its `copySelection` callback, which
	// requests this. Accepted only right after the launcher itself saw the mouse release ending a drag or multi-click in the raw input (one copy per
	// gesture); no consent screen. pi-tui then flashes "Copied!" (or the failure) from the answer.
	actions["copy.selection"] = LauncherAction{
		Kind:   GrantGesture,
		Accept: func(payload any) bool { _, ok := copyText(payload); return ok },
		Run: func(payload any, ctx ActionContext) (any, error) {
			text, _ := copyText(payload)
			return nil, clipboard(text, ctx.WriteTerminal)
		},
	}
	// C: `/copy`, a login URL, a share link. Payload `{text, label?}`; the screen shows the full text.
	actions["copy"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, _ ConsentContext) *ConsentRequest {
			text, ok := copyText(payload)
			if !ok {
				return nil
			}
			var facts []ConsentFact
			if label := reported(record(payload)["label"]); label != "" {
				facts = []ConsentFact{{"What", label}}
			}
			return &ConsentRequest{Action: fmt.Sprintf("Copy %s characters to the clipboard", count(utf16Len(text))), Facts: facts, Preview: text, HasPreview: true, ConfirmLabel: "copy"}
		},
		Run: func(payload any, ctx ActionContext) (any, error) {
			text, _ := copyText(payload)
			return nil, copied(text, ctx)
		},
	}
	// C: `{kind: "external", url}` (https only) or `{kind: "origin", path}` (allowlisted paths on this origin).
	actions["openUrl"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, ctx ConsentContext) *ConsentRequest {
			resolved := resolveOpen(record(payload), sessionOf(ctx.Target))
			if resolved == nil {
				return nil
			}
			consent := &ConsentRequest{Action: "Open this external page in your browser", Preview: resolved.url.String(), HasPreview: true, ConfirmLabel: "open"}
			if !resolved.external {
				consent.Action = "Open this page of the server in your browser"
				consent.Facts = []ConsentFact{{"Note", "The browser needs its own sign-in (an owner link or a share link); nothing is passed on."}}
			}
			return consent
		},
		Run: func(payload any, ctx ActionContext) (any, error) {
			resolved := resolveOpen(record(payload), sessionOf(ctx.Grant.Target))
			if resolved == nil {
				return nil, errors.New("Invalid URL")
			}
			open(resolved.url.String())
			return nil, nil
		},
	}
	// C: `{kind: "file", session, id, name?}` (sent files) or `{kind: "artifact", session, name, version}`.
	actions["download"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, ctx ConsentContext) *ConsentRequest {
			resolved := resolveDownload(record(payload), sessionOf(ctx.Target))
			if resolved == nil {
				return nil
			}
			var facts []ConsentFact
			if label := reported(resolved.label); label != "" {
				facts = append(facts, ConsentFact{"File", label})
			}
			facts = append(facts, ConsentFact{"Folder", folder}, ConsentFact{"Limit", "25 MiB; an existing file is never overwritten"})
			return &ConsentRequest{Action: "Download a file from the server to this computer", Facts: facts, ConfirmLabel: "download"}
		},
		Run: func(payload any, ctx ActionContext) (any, error) {
			resolved := resolveDownload(record(payload), sessionOf(ctx.Grant.Target))
			if resolved == nil {
				return nil, errors.New("Invalid download")
			}
			timed, cancel := context.WithTimeout(ctx.Grant.Context, 60*time.Second)
			defer cancel()
			response, err := request(timed, resolved.path, Request{})
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode > 299 {
				return nil, fmt.Errorf("The server refused the download (%d)", response.StatusCode)
			}
			if response.ContentLength > MaxDownload {
				return nil, errors.New("Download exceeds 25 MiB")
			}
			name := dispositionName(response.Header.Get("Content-Disposition"))
			if name == "" {
				name = resolved.fallback
			}
			path, err := saveBounded(timed, response.Body, folder, SafeFileName(name))
			if err != nil {
				return nil, err
			}
			// The local path stays in the launcher: the worker learns only that it was saved.
			ctx.UI.Notice("Saved "+path, 2500*time.Millisecond)
			return map[string]any{"saved": true}, nil
		},
	}
	// K (Ctrl+G): edit the editor's text in the visitor's editor; the result goes back only if the grant holds.
	actions["editor.external"] = LauncherAction{Kind: GrantK, Key: "app.editor.external", RunKey: func(ctx ActionContext) error {
		argv := options.Editor
		if len(argv) == 0 {
			ctx.UI.Notice("No external editor: set externalEditor in pi's settings.json, $VISUAL or $EDITOR", 3*time.Second)
			return nil
		}
		answer, err := ctx.Ask("editorText", nil)
		if err != nil {
			return err
		}
		text, ok := record(answer)["text"].(string)
		if !ok || len(text) > maxEditor {
			ctx.UI.Notice("Nothing to edit here", 1200*time.Millisecond)
			return nil
		}
		directory, err := os.MkdirTemp("", "p2-editor-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(directory)
		file := filepath.Join(directory, "prompt.md")
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			return err
		}
		var result *string
		var code int
		var ran bool
		if err := ctx.ReleaseTerminal(func() error { code, ran = runEditor(argv, file); return nil }); err != nil {
			return err
		}
		if ran && code == 0 {
			if edited, err := os.ReadFile(file); err == nil && len(edited) <= maxEditor {
				text := strings.TrimPrefix(string(edited), "\ufeff")
				text = strings.TrimSuffix(text, "\n")
				result = &text
			}
		}
		if result == nil {
			ctx.UI.Notice("The editor failed; the text is unchanged", 1200*time.Millisecond)
			return nil
		}
		// `Ask` refuses once the grant ended (navigation or a worker swap): the result is dropped.
		_, err = ctx.Ask("editorResult", map[string]any{"text": *result})
		return err
	}}
	// K (Ctrl+Z): job control; modes are undone first and restored on resume.
	actions["suspend"] = LauncherAction{Kind: GrantK, Key: "app.suspend", RunKey: func(ctx ActionContext) error {
		if platform == "win32" || platform == "windows" {
			ctx.UI.Notice("Suspend to background is not supported on Windows", 1200*time.Millisecond)
			return nil
		}
		return ctx.ReleaseTerminal(suspend)
	}}
	// C (`/settings`, theme picker, `/keybindings`): `{patch}` for p2's own `prefs.json`. Refused before anything is shown when it touches launcher-owned
	// bindings or anything but keybindings, theme and whitelisted settings (the editor command, the mouse policy, the download folder). The screen shows
	// the change as a diff of the file.
	actions["savePrefs"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, _ ConsentContext) *ConsentRequest {
			patch, problem := ValidatePrefsPatch(record(payload)["patch"])
			if problem != "" {
				return nil
			}
			current, err := ReadPrefsFile(prefsDirectory)
			if err != nil {
				return nil
			}
			diff := PrefsDiff(current, ApplyPrefsPatch(current, patch))
			if len(diff) == 0 {
				return nil
			}
			return &ConsentRequest{Action: "Save your TUI preferences on this computer", Facts: []ConsentFact{{"File", filepath.Join(prefsDirectory, "prefs.json")}}, Preview: strings.Join(diff, "\n"), HasPreview: true, ConfirmLabel: "save"}
		},
		Run: func(payload any, _ ActionContext) (any, error) {
			patch, problem := ValidatePrefsPatch(record(payload)["patch"])
			if problem != "" {
				return nil, errors.New(problem)
			}
			// Re-read: the file may have changed while the screen was up; the patch applies to what is there now.
			current, err := ReadPrefsFile(prefsDirectory)
			if err != nil {
				return nil, err
			}
			return map[string]any{"saved": true}, WritePrefsFile(prefsDirectory, ApplyPrefsPatch(current, patch))
		},
	}
	// C (owner): `{action: "reload"}` on the dev channel `/__p2_dev`, with the launcher's own sign-in.
	actions["devCommand"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, _ ConsentContext) *ConsentRequest {
			value := record(payload)
			if options.Role == "owner" && value["action"] == "reload" && len(value) == 1 {
				return &ConsentRequest{Action: "Reload the server's changed plugins (dev server)", ConfirmLabel: "reload"}
			}
			return nil
		},
		Run: func(_ any, ctx ActionContext) (any, error) {
			timed, cancel := context.WithTimeout(ctx.Grant.Context, 120*time.Second)
			defer cancel()
			response, err := request(timed, "/__p2_dev", Request{Method: http.MethodPost, Headers: map[string]string{"Content-Type": "application/json"}, Body: strings.NewReader(`{"action":"reload"}`)})
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			data, _ := io.ReadAll(io.LimitReader(response.Body, 2000))
			message := ""
			var value map[string]any
			if json.Unmarshal(data, &value) == nil {
				message, _ = value["message"].(string)
			}
			if response.StatusCode < 200 || response.StatusCode > 299 {
				if message == "" {
					message = fmt.Sprintf("The server refused (%d)", response.StatusCode)
				}
				return nil, errors.New(message)
			}
			return map[string]any{"message": message}, nil
		},
	}
	// C (owner): `{all}` signs out this device's cookie, or every owner device. The cookie is then forgotten.
	actions["signOut"] = LauncherAction{
		Kind: GrantC,
		Consent: func(payload any, _ ConsentContext) *ConsentRequest {
			all, isBool := record(payload)["all"].(bool)
			if options.Role != "owner" || !isBool {
				return nil
			}
			// Loopback is the device owner by address; it has no cookie to end.
			if !all && (loopback || options.Credentials.Cookie == "") {
				return nil
			}
			if all {
				return &ConsentRequest{Action: "Sign out ALL owner devices, this one included", Facts: []ConsentFact{{"Effect", "Every owner cookie stops working; each device needs a new owner link."}}, ConfirmLabel: "sign out"}
			}
			return &ConsentRequest{Action: "Sign out this device", Facts: []ConsentFact{{"Effect", "This launcher's sign-in ends; it asks for an owner link again."}}, ConfirmLabel: "sign out"}
		},
		Run: func(payload any, ctx ActionContext) (any, error) {
			all, _ := record(payload)["all"].(bool)
			path := "/owner/logout"
			if all {
				path = "/owner/logout-all"
			}
			timed, cancel := context.WithTimeout(ctx.Grant.Context, 10*time.Second)
			defer cancel()
			response, err := request(timed, path, Request{Method: http.MethodPost})
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			if response.StatusCode < 200 || response.StatusCode > 299 {
				return nil, fmt.Errorf("The server refused (%d)", response.StatusCode)
			}
			if options.OnSignedOut != nil {
				options.OnSignedOut(all)
			}
			return nil, nil
		},
	}
	return actions
}
