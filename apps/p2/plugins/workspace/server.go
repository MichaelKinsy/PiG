package workspace

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/google/uuid"
)

// Ports apps/p2/plugins/workspace/server.ts: the HTTP routes of the workspace (`GET`/`HEAD /workspace-file` streams a file
// of the workspace, with media ranges; `POST /workspace-upload` streams one into a folder), through `WorkspaceTransfer` of
// the session's worker, which answers only the server. A request is reauthorized at every use.

var rangePattern = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)

// maxSafeInteger is `Number.MAX_SAFE_INTEGER`.
const maxSafeInteger = 1<<53 - 1

// disallowed is the characters a download's file name loses to `_`.
var disallowed = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// uploadChunk is how much of an upload one `write` carries.
const uploadChunk = contract.WorkspaceReadChunk

func sendJSON(response http.ResponseWriter, status int, value delta.JsonValue) error {
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		return err
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, err = response.Write(encoded)
	return err
}

// encodeURIComponent is JavaScript's.
func encodeURIComponent(text string) string {
	const unreserved = "-_.!~*'()"
	var out strings.Builder
	for _, b := range []byte(text) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte(unreserved, b) >= 0 {
			out.WriteByte(b)
		} else {
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}

// parameter is the value of a query parameter: its first, "" when absent.
func parameter(params url.Values, name string) string { return params.Get(name) }

// refused is a request the route answers with its status and plain text.
type refused struct {
	status  int
	message string
}

func (r *refused) Error() string { return r.message }

// byteRange is a request's `Range` as the read's bounds: Start, End (inclusive) and Suffix, nil when absent.
type byteRange struct{ start, end, suffix *int64 }

// parseRange reads a `Range` header: ok false for one the route refuses (416).
func parseRange(header string) (bounds byteRange, ok bool) {
	match := rangePattern.FindStringSubmatch(header)
	if match == nil || (match[1] == "" && match[2] == "") {
		return bounds, false
	}
	number := func(digits string) (int64, bool) {
		value, err := strconv.ParseFloat(digits, 64)
		if err != nil || value > maxSafeInteger {
			return 0, false
		}
		return int64(value), true
	}
	if match[1] != "" {
		start, valid := number(match[1])
		if !valid {
			return bounds, false
		}
		bounds.start = &start
		if match[2] != "" {
			end, valid := number(match[2])
			if !valid || end < start {
				return bounds, false
			}
			bounds.end = &end
		}
		return bounds, true
	}
	suffix, valid := number(match[2])
	if !valid || suffix == 0 {
		return bounds, false
	}
	bounds.suffix = &suffix
	return bounds, true
}

func (b byteRange) json(path string) *delta.JsonObject {
	request := delta.JsonObjectOf("path", path)
	if b.start != nil {
		request.Set("start", float64(*b.start))
	}
	if b.end != nil {
		request.Set("end", float64(*b.end))
	}
	if b.suffix != nil {
		request.Set("suffix", float64(*b.suffix))
	}
	return request
}

func serveRoutes(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.workspace-routes", func(self host.StoreSelf) error {
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		server := p2runtime.ServerOf(self)
		serve := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, outer host.Ctx) error {
			params := request.URL.Query()
			session := parameter(params, "session")
			uploading := request.Method == http.MethodPost
			if !p2runtime.CanSend(access.Principal, session) || request.Header.Get("Sec-Fetch-Site") == "cross-site" ||
				(uploading && !p2runtime.FromOrigin(request, access.Origin.Origin)) {
				_, _ = io.Copy(io.Discard, request.Body)
				response.WriteHeader(http.StatusForbidden)
				_, err := io.WriteString(response, "Forbidden")
				return err
			}
			headersSent := false
			err := func() error {
				parts, err := server()
				if err != nil {
					return err
				}
				// A root of the server's own (the worker answers only the server), ended by a disconnect.
				abortable := chordctx.WithAbortSignal(outer, request.Context())
				_, err = parts.Root(func(ctx host.Ctx) (any, error) {
					info, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
					if err != nil {
						return nil, err
					}
					if objectOf(info).Value("archivedAt") != nil {
						return nil, errors.New("Session is archived")
					}
					conversationText := "1"
					if params.Has("conversation") {
						conversationText = parameter(params, "conversation")
					}
					conversation, parseErr := strconv.ParseFloat(strings.TrimSpace(conversationText), 64)
					path, validPath := contract.WorkspacePath(parameter(params, "path"))
					if !validPath || parseErr != nil || conversation != math.Trunc(conversation) || conversation < 1 || conversation > maxSafeInteger {
						return nil, errors.New("Invalid workspace location")
					}
					transfer, err := ctx.Get(contract.WorkspaceTransfer, delta.JsonObjectOf("session", session, "conversation", conversation), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					if uploading {
						return nil, upload(response, request, parts, transfer, ctx, session, conversation, path, parameter(params, "name"))
					}
					return nil, download(response, request, transfer, ctx, path, &headersSent)
				}, abortable)
				return err
			}()
			if err == nil {
				return nil
			}
			if uploading {
				_, _ = io.Copy(io.Discard, request.Body)
			}
			if headersSent {
				// `response.destroy()`: the transfer ends mid-body, the client sees a broken one.
				if conn, _, hijackErr := http.NewResponseController(response).Hijack(); hijackErr == nil {
					_ = conn.Close()
				}
				return nil
			}
			var plain *refused
			if errors.As(err, &plain) {
				response.Header().Set("Content-Type", "text/plain")
				response.WriteHeader(plain.status)
				_, writeErr := io.WriteString(response, plain.message)
				return writeErr
			}
			status := http.StatusNotFound
			if uploading {
				status = http.StatusBadRequest
			}
			message := err.Error()
			if message == "" {
				message = "Workspace request failed"
			}
			return sendJSON(response, status, delta.JsonObjectOf("error", message))
		}
		p2runtime.ContributeKeyed(self, p2runtime.Routes,
			p2runtime.Entry("GET /workspace-file", p2runtime.RouteHandler(serve)),
			p2runtime.Entry("HEAD /workspace-file", p2runtime.RouteHandler(serve)),
			p2runtime.Entry("POST /workspace-upload", p2runtime.RouteHandler(serve)),
		)
		return nil
	})
}

// upload streams the request's body into folder path of the workspace as name, in chunks the worker appends to a private
// file and renames into place at the end; a failure cleans up in a root of its own, which a disconnect does not abort.
func upload(response http.ResponseWriter, request *http.Request, server *p2runtime.ServerParts, transfer *host.Reference, ctx host.Ctx, session string, conversation float64, path, name string) error {
	if name == "" || p2runtime.JSLength(name) > 255 || strings.Contains(name, "/") {
		return errors.New("Invalid file name")
	}
	if _, valid := contract.WorkspacePath(name); !valid {
		return errors.New("Invalid file name")
	}
	token := uuid.NewString()
	offset := 0
	err := func() error {
		buffer := make([]byte, uploadChunk)
		for {
			read, err := request.Body.Read(buffer)
			if read > 0 {
				if offset+read > contract.WorkspaceDownloadLimit {
					return errors.New("Upload exceeds 25 MiB")
				}
				if _, err := transfer.Call("write", delta.JsonObjectOf("path", path, "name", name, "token", token, "offset", float64(offset),
					"data", base64.StdEncoding.EncodeToString(buffer[:read]), "final", false), ctx); err != nil {
					return err
				}
				offset += read
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
		_, err := transfer.Call("write", delta.JsonObjectOf("path", path, "name", name, "token", token, "offset", float64(offset), "data", "", "final", true), ctx)
		return err
	}()
	if err != nil {
		// Cleanup has its own uncancelled root after a disconnect.
		_, _ = server.Root(func(cleanup host.Ctx) (any, error) {
			again, err := cleanup.Get(contract.WorkspaceTransfer, delta.JsonObjectOf("session", session, "conversation", conversation), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			return again.Call("write", delta.JsonObjectOf("path", path, "name", name, "token", token, "offset", float64(offset), "data", "", "final", false, "abort", true), cleanup)
		}, chordctx.WithoutAbortSignal(ctx))
		return err
	}
	joined := name
	if path != "" {
		joined = path + "/" + name
	}
	return sendJSON(response, http.StatusOK, delta.JsonObjectOf("path", joined, "size", float64(offset)))
}

func download(response http.ResponseWriter, request *http.Request, transfer *host.Reference, ctx host.Ctx, path string, headersSent *bool) error {
	if path == "" {
		return errors.New("Invalid file path")
	}
	preview, previewed := p2runtime.PreviewOf(path)
	media := previewed && (preview.Kind == p2runtime.PreviewAudio || preview.Kind == p2runtime.PreviewVideo)
	header, hasHeader := "", false
	if media {
		header = request.Header.Get("Range")
		hasHeader = header != ""
	}
	var bounds byteRange
	if hasHeader {
		var valid bool
		if bounds, valid = parseRange(header); !valid {
			response.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			*headersSent = true
			return nil
		}
	}
	read := func(request *delta.JsonObject) (size, start int64, data []byte, err error) {
		chunk, err := transfer.Call("read", request, ctx)
		if err != nil {
			return 0, 0, nil, err
		}
		object := objectOf(chunk)
		size, _ = int64Of(object.Value("size"))
		start, _ = int64Of(object.Value("start"))
		data, err = base64.StdEncoding.DecodeString(stringOf(object.Value("data")))
		return size, start, data, err
	}
	size, start, data, err := read(bounds.json(path))
	if err != nil {
		return err
	}
	end := size - 1
	if bounds.end != nil {
		end = min(*bounds.end, size-1)
	}
	if hasHeader && (size == 0 || start >= size || end < start) {
		response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		response.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		*headersSent = true
		return nil
	}
	// As Pi Mobile: a whole file over 25 MiB is refused (media players page through ranges instead).
	if !hasHeader && size > contract.WorkspaceDownloadLimit {
		return &refused{status: http.StatusRequestEntityTooLarge, message: "File too large"}
	}
	name := path[strings.LastIndex(path, "/")+1:]
	// Only media keeps its type (for the viewer's image, audio and video elements); anything else is an opaque download,
	// never a document the browser renders.
	typed := previewed && (preview.Kind == p2runtime.PreviewImage || media)
	mime := "application/octet-stream"
	if typed {
		mime = preview.MimeType
	}
	length := int64(0)
	if size != 0 {
		length = end - start + 1
	}
	headers := response.Header()
	headers.Set("Content-Type", mime)
	headers.Set("Content-Length", strconv.FormatInt(length, 10))
	headers.Set("Content-Disposition", `attachment; filename="`+disallowed.ReplaceAllString(name, "_")+`"; filename*=UTF-8''`+encodeURIComponent(name))
	headers.Set("Accept-Ranges", map[bool]string{true: "bytes", false: "none"}[media])
	headers.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	headers.Set("Cross-Origin-Resource-Policy", "same-origin")
	status := http.StatusOK
	if hasHeader {
		status = http.StatusPartialContent
		headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
	}
	response.WriteHeader(status)
	*headersSent = true
	if request.Method == http.MethodHead || size == 0 {
		return nil
	}
	for offset := start; offset <= end; {
		if len(data) == 0 {
			return errors.New("File changed during transfer")
		}
		if _, err := response.Write(data); err != nil {
			return err
		}
		offset += int64(len(data))
		if offset <= end {
			if _, _, data, err = read(delta.JsonObjectOf("path", path, "start", float64(offset), "end", float64(end))); err != nil {
				return err
			}
		}
	}
	return nil
}

func int64Of(value any) (int64, bool) {
	number, ok := value.(float64)
	return int64(number), ok
}

// ServerFacet contributes the workspace's HTTP routes.
var ServerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error { return serveRoutes(facet) })
