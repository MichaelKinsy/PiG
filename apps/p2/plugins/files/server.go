package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/files/server.ts: uploads (POST /upload) and download snapshots (GET /download) of a session's
// files, which live in the session's worker (its `Files`): the routes check who may upload or view and stream the bytes
// through `SessionFiles` as the server. The server's mirror of each session's file summaries (`FileSummaries`), pushed by
// the worker.
//
// Forced Go differences. A Node `IncomingMessage` async iterator is the request body read in pieces of at most 1 MiB; the
// download's client going away is the request's context (`response.on("close")`); `response.destroy()` after the headers
// went out closes the connection.

// byteRange is the requested byte range of a seekable (audio/video) file: ok false for all of it, invalid for a range
// that is none.
func byteRange(header string, size int) (start, end int, ranged, valid bool) {
	if header == "" || size == 0 {
		return 0, size - 1, false, true
	}
	rest, found := strings.CutPrefix(header, "bytes=")
	first, last, dash := strings.Cut(rest, "-")
	if !found || !dash || !digits(first) || !digits(last) {
		return 0, 0, true, false
	}
	start, end = 0, size-1
	number := func(text string) (int, bool) {
		value, err := strconv.ParseInt(text, 10, 64)
		return int(value), err == nil && value <= 1<<53-1
	}
	switch {
	case first == "" && last != "":
		value, ok := number(last)
		if !ok {
			return 0, 0, true, false
		}
		start = max(0, size-value)
	case first != "":
		value, ok := number(first)
		if !ok {
			return 0, 0, true, false
		}
		start = value
		if last != "" {
			end, ok = number(last)
			if !ok {
				return 0, 0, true, false
			}
		}
	}
	if start < 0 || end < start || start >= size {
		return 0, 0, true, false
	}
	return start, min(end, size-1), true, true
}

func digits(text string) bool {
	for _, character := range text {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func mustState(name string, options documents.StateOptions) *documents.StateDefinition {
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

func mustLocal(name string, declaration contracts.LocalDeclaration) *contracts.Contract {
	contract, err := contracts.Local(name, declaration)
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	// summaries is every session's file summaries, by session (persisted: they show while the session's worker is stopped).
	summaries = mustState("p2.file-summaries", documents.StateOptions{
		Schema:  p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("spaces", schema.Map(contract.SpaceSummarySchema))})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("spaces", delta.NewJsonObject(0)) },
		Keyed:   true,
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 20
		},
	})
	// summaryIndex is the mirror's store (server, host-internal): one document per session.
	summaryIndex = mustLocal("p2.file-summary-index", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("push", contracts.NewMethod(p2runtime.Contract(schema.Obj(schema.Properties{
				schema.Prop("session", schema.Str()), schema.Prop("space", schema.Str()), schema.Prop("summary", contract.SpaceSummarySchema),
			})))),
			contracts.Mem("of", contracts.NewMethod(
				p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("session", schema.Str())})),
				p2runtime.Contract(schema.Map(contract.SpaceSummarySchema)),
			)),
		},
	})
)

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func objectArgs(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func hostCtx(ctx context.Context) host.Ctx { return ctx.(host.Ctx) }

func serveSummaryIndex(facet host.FacetHost) error {
	return facet.Serve(summaryIndex, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		store := self.State(summaries).(*documents.Keyed)
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"push": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				_, err := store.Change(ctx, stringOf(in.Value("session")), func(draft *delta.Object) (any, error) {
					return nil, draft.Object("spaces").Set(stringOf(in.Value("space")), in.Value("summary"))
				})
				return nil, err
			},
			"of": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				found, err := store.Get(ctx, stringOf(objectArgs(args).Value("session")))
				if err != nil {
					return nil, err
				}
				if found == nil {
					return delta.NewJsonObject(0), nil
				}
				return found.Value("spaces"), nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

func serveFileSummaries(facet host.FacetHost) error {
	return facet.Serve(contract.FileSummaries, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("file summaries are for the owner and session workers")
		}
		index := self.Get(summaryIndex, delta.NewJsonObject(0))
		switch typed := who.(type) {
		case p2runtime.WorkerPrincipal:
			// A worker pushes its own session's only: the session comes from its principal, never from arguments.
			return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"push": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					return index.Call("push", delta.JsonObjectOf("session", typed.Session, "space", in.Value("space"), "summary", in.Value("summary")), ctx)
				},
			}}}, nil
		case p2runtime.OwnerPrincipal:
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"of": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return index.Call("of", args, ctx)
				},
			}}}, nil
		}
		return contracts.Implementation{}, p2runtime.Forbidden("file summaries are for the owner and session workers")
	}), host.ServeOptions{})
}

// archived is whether catalog row is of an archived session.
func archived(row delta.JsonValue) bool { return objectOf(row).Value("archivedAt") != nil }

func serveDownloadStore(facet host.FacetHost) error {
	return facet.Serve(contract.DownloadStore, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			// A local object's method acts as the server: the session's worker answers it.
			"save": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				session, path := stringOf(in.Value("session")), stringOf(in.Value("path"))
				// An archived session's worker never starts: refused at once, never waited for.
				row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
				if err != nil {
					return nil, err
				}
				if archived(row) {
					return nil, &p2runtime.DownloadError{Status: 409, Message: "Session is archived"}
				}
				// O_NONBLOCK avoids hanging on a named pipe before fstat rejects non-regular sources.
				source, err := openFile(path)
				if err != nil {
					return nil, &p2runtime.DownloadError{Status: 400, Message: "Could not open source file"}
				}
				defer source.Close()
				stat, err := source.Stat()
				if err != nil {
					return nil, err
				}
				if !stat.Mode().IsRegular() {
					return nil, &p2runtime.DownloadError{Status: 400, Message: "Source must be a regular file"}
				}
				if stat.Size() > p2runtime.MaxDownloadBytes {
					return nil, &p2runtime.DownloadError{Status: 413, Message: "File exceeds 25 MiB"}
				}
				files, err := hostCtx(ctx).Get(contract.SessionFiles, delta.JsonObjectOf("session", session), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				name := filepath.Base(path)
				if given, ok := in.Value("name").(string); ok {
					name = given
				}
				begun, err := files.Call("begin", delta.JsonObjectOf("space", p2runtime.DownloadsSpace, "name", name, "source", "server"), ctx)
				if err != nil {
					return nil, err
				}
				upload := stringOf(objectOf(begun).Value("upload"))
				saved, err := func() (delta.JsonValue, error) {
					buffer := make([]byte, p2runtime.FileChunk)
					for offset := 0; ; {
						read, err := source.Read(buffer)
						if read > 0 {
							if offset+read > p2runtime.MaxDownloadBytes {
								return nil, &p2runtime.DownloadError{Status: 413, Message: "File exceeds 25 MiB"}
							}
							if _, err := files.Call("append", delta.JsonObjectOf("upload", upload, "offset", float64(offset), "bytes", append([]byte(nil), buffer[:read]...)), ctx); err != nil {
								return nil, err
							}
							offset += read
						}
						if errors.Is(err, io.EOF) {
							break
						}
						if err != nil {
							return nil, err
						}
					}
					return files.Call("finish", upload, ctx)
				}()
				if err != nil {
					_, _ = files.Call("cancel", upload, ctx)
					return nil, err
				}
				return delta.JsonObjectOf("id", objectOf(saved).Value("id"), "name", objectOf(saved).Value("name"), "size", objectOf(saved).Value("size")), nil
			},
		}}}, nil
	}), host.ServeOptions{})
}

// asciiName is name with every UTF-16 unit outside printable ASCII replaced by `_` (`replace(/[^\x20-\x7e]/g, "_")`).
func asciiName(name string) string {
	var out strings.Builder
	for _, character := range name {
		if character >= 0x20 && character <= 0x7e {
			out.WriteRune(character)
		} else {
			out.WriteString(strings.Repeat("_", len(utf16.Encode([]rune{character}))))
		}
	}
	return out.String()
}

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

func serveRoutes(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.file-routes", func(self host.StoreSelf) error {
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		server := p2runtime.ServerOf(self)
		upload := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, outer host.Ctx) error {
			uploaded, err := func() (delta.JsonValue, error) {
				// A state-changing request: exactly same-origin (a cross-site form cannot send this Origin).
				if !p2runtime.FromOrigin(request, access.Origin.Origin) {
					return nil, &UploadError{403, "Invalid origin"}
				}
				query := request.URL.Query()
				session := query.Get("session")
				if !p2runtime.CanSend(access.Principal, session) {
					return nil, &UploadError{403, "Forbidden"}
				}
				name := query.Get("name")
				if name == "" || len(utf16.Encode([]rune(name))) > 255 {
					return nil, &UploadError{400, "Invalid file name"}
				}
				if declared, err := strconv.ParseFloat(strings.TrimSpace(request.Header.Get("Content-Length")), 64); err == nil && request.Header.Get("Content-Length") != "" && declared > MaxUploadBytes {
					return nil, &UploadError{413, "File too large"}
				}
				source := "owner"
				if guest, ok := access.Principal.(p2runtime.GuestPrincipal); ok {
					source = "guest:" + guest.ID
				}
				parts, err := server()
				if err != nil {
					return nil, err
				}
				// A root of the server's own (the worker's `SessionFiles` answers only the server).
				result, err := parts.Root(func(ctx host.Ctx) (any, error) {
					// An archived session's worker never starts: refused at once, never waited for.
					row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
					if err != nil {
						return nil, &UploadError{400, "Unknown session"}
					}
					if archived(row) {
						return nil, &UploadError{409, "Session is archived"}
					}
					files, err := ctx.Get(contract.SessionFiles, delta.JsonObjectOf("session", session), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					begun, err := files.Call("begin", delta.JsonObjectOf("space", p2runtime.UploadsSpace, "name", name, "source", source), ctx)
					if err != nil {
						return nil, err
					}
					id := stringOf(objectOf(begun).Value("upload"))
					finished, err := func() (delta.JsonValue, error) {
						buffer := make([]byte, p2runtime.FileChunk)
						for offset := 0; ; {
							read, err := request.Body.Read(buffer)
							if read > 0 {
								if offset+read > MaxUploadBytes {
									return nil, &UploadError{413, "File too large"}
								}
								if _, err := files.Call("append", delta.JsonObjectOf("upload", id, "offset", float64(offset), "bytes", append([]byte(nil), buffer[:read]...)), ctx); err != nil {
									return nil, err
								}
								offset += read
							}
							if errors.Is(err, io.EOF) {
								break
							}
							if err != nil {
								return nil, err
							}
						}
						return files.Call("finish", id, ctx)
					}()
					if err != nil {
						_, _ = files.Call("cancel", id, ctx)
						return nil, err
					}
					return finished, nil
				}, outer)
				if err != nil {
					return nil, err
				}
				return result.(delta.JsonValue), nil
			}()
			if err != nil {
				_, _ = io.Copy(io.Discard, request.Body)
				status := http.StatusBadRequest
				var refused *UploadError
				if errors.As(err, &refused) {
					status = refused.Status
				}
				return sendJSON(response, status, delta.JsonObjectOf("error", err.Error(), "limit", float64(MaxUploadBytes)))
			}
			return sendJSON(response, http.StatusOK, uploaded)
		}
		download := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, outer host.Ctx) error {
			query := request.URL.Query()
			session := query.Get("session")
			// A cross-site page may link here, but never with the owner's or a guest's access.
			if request.Header.Get("Sec-Fetch-Site") == "cross-site" || !p2runtime.CanView(access.Principal, session) {
				response.Header().Set("Content-Type", "text/plain")
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
				// The client going away aborts the transfer.
				abortable := chordctx.WithAbortSignal(outer, request.Context())
				_, err = parts.Root(func(ctx host.Ctx) (any, error) {
					// An unknown or archived session (whose worker never starts) is a 404 at once, never waited for.
					row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
					if err != nil || archived(row) {
						return nil, &p2runtime.DownloadError{Status: 404, Message: "File not found"}
					}
					files, err := ctx.Get(contract.SessionFiles, delta.JsonObjectOf("session", session), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					found, err := files.Call("download", query.Get("id"), ctx)
					if err != nil {
						return nil, err
					}
					info := objectOf(found)
					if info == nil {
						return nil, &p2runtime.DownloadError{Status: 404, Message: "File not found"}
					}
					name, size := stringOf(info.Value("name")), int(info.Value("size").(float64))
					preview, previewed := p2runtime.PreviewOf(name)
					seekable := previewed && (preview.Kind == p2runtime.PreviewAudio || preview.Kind == p2runtime.PreviewVideo)
					start, end, ranged := 0, max(0, size-1), false
					if seekable {
						var valid bool
						start, end, ranged, valid = byteRange(request.Header.Get("Range"), size)
						if !valid {
							response.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
							response.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
							return nil, nil
						}
						if !ranged {
							start, end = 0, max(0, size-1)
						}
					}
					mime := "application/octet-stream"
					if previewed {
						mime = preview.MimeType
					}
					length := 0
					if size != 0 {
						length = end - start + 1
					}
					header := response.Header()
					header.Set("Content-Type", mime)
					header.Set("Content-Length", strconv.Itoa(length))
					header.Set("Content-Disposition", "attachment; filename=\""+asciiName(name)+"\"; filename*=UTF-8''"+encodeURIComponent(name))
					header.Set("Accept-Ranges", map[bool]string{true: "bytes", false: "none"}[seekable])
					status := http.StatusOK
					if ranged {
						status = http.StatusPartialContent
						header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
					}
					response.WriteHeader(status)
					headersSent = true
					if request.Method == http.MethodHead || size == 0 {
						return nil, nil
					}
					for offset := start; offset <= end; {
						chunk := min(p2runtime.FileChunk, end-offset+1)
						value, err := files.Call("read", delta.JsonObjectOf("file", info.Value("file"), "offset", float64(offset), "length", float64(chunk)), ctx)
						if err != nil {
							return nil, err
						}
						bytes, _ := value.([]byte)
						if len(bytes) == 0 {
							return nil, errors.New("File changed during transfer")
						}
						if _, err := response.Write(bytes); err != nil {
							return nil, err
						}
						offset += len(bytes)
					}
					return nil, nil
				}, abortable)
				return err
			}()
			if err == nil {
				return nil
			}
			if headersSent {
				// `response.destroy()`: the transfer ends mid-body, the client sees a broken one.
				if conn, _, hijackErr := http.NewResponseController(response).Hijack(); hijackErr == nil {
					_ = conn.Close()
				}
				return nil
			}
			status := http.StatusNotFound
			var failed *p2runtime.DownloadError
			if errors.As(err, &failed) {
				status = failed.Status
			}
			response.Header().Set("Content-Type", "text/plain")
			response.WriteHeader(status)
			_, writeErr := io.WriteString(response, "File not found")
			return writeErr
		}
		p2runtime.ContributeKeyed(self, p2runtime.Routes,
			p2runtime.Entry("POST /upload", p2runtime.RouteHandler(upload)),
			p2runtime.Entry("GET /download", p2runtime.RouteHandler(download)),
			p2runtime.Entry("HEAD /download", p2runtime.RouteHandler(download)),
		)
		return nil
	})
}

// sendJSON writes a JSON response of a Chord value: its keys in the order they were set (`JSON.stringify`).
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

// Server is the plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveSummaryIndex, serveFileSummaries, serveDownloadStore, serveRoutes} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
