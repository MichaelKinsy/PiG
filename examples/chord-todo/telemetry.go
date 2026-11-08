package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// telemetryFile is the telemetry store's file in the data directory dataDir.
func telemetryFile(dataDir string) string { return filepath.Join(dataDir, "telemetry.sqlite") }

// openTelemetry opens the telemetry store at file for this process (its directory made, private, when missing): a recorder, which is
// the adapter the server records to and the store's read interface. The server is its only writer; the file's readers open it
// read-only (`telemetrysqlite.OpenTelemetryStore`).
func openTelemetry(file string) (*telemetrysqlite.SqliteTelemetry, error) {
	// Spans carry who called what: the data directory is private before the store's first file appears in it.
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	db, err := node.OpenNodeSqliteDatabase(file, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return nil, err
	}
	store, err := telemetrysqlite.CreateSqliteTelemetry(db, telemetrysqlite.SqliteTelemetryOptions{})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// principalAttributes is who a span was for, as the app's telemetry records it (the host's `PrincipalAttributes` hook): a short
// principal key, and a user's name, which the owner's trace viewer shows.
func principalAttributes(principal data.Principal) telemetry.SpanAttributes {
	object := principal
	switch object.Value("kind") {
	case "user":
		return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "user:"+fmt.Sprint(object.Value("id"))), telemetry.Attr("user.name", object.Value("name"))}
	case "worker":
		return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "worker:"+fmt.Sprint(object.Value("list")))}
	default:
		return telemetry.SpanAttributes{telemetry.Attr("chord.principal", fmt.Sprint(object.Value("kind")))}
	}
}

// traceCommand is `chord-todo trace <data directory> [--name <span>] [--principal <key>]` or `... <trace id>`: it reads the telemetry
// store of a data directory through the store's read interface, while the app runs or after. Without a trace id it prints the newest
// traces (those whose first span is `--name`, and with a span for `--principal`, when given) and the bytes on the links; with one,
// that trace as the renderer prints it.
func traceCommand(args []string, out interface{ Write([]byte) (int, error) }) error {
	var dataDir, id, name, principal string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--name" && i+1 < len(args):
			i++
			name = args[i]
		case args[i] == "--principal" && i+1 < len(args):
			i++
			principal = args[i]
		case dataDir == "":
			dataDir = args[i]
		default:
			id = args[i]
		}
	}
	if dataDir == "" {
		return fmt.Errorf("usage: trace <data directory> [--name <span>] [--principal <key>] [<trace id>]")
	}
	// The shared file, read-only: the app's processes may be writing it meanwhile.
	store, err := telemetrysqlite.OpenTelemetryStore(telemetryFile(dataDir))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	if id != "" {
		trace, err := store.GetTrace(id)
		switch {
		case err != nil:
			return err
		case trace == nil:
			_, err = fmt.Fprintf(out, "no trace %s\n", id)
		default:
			_, err = fmt.Fprintln(out, telemetry.RenderTrace(trace.Spans))
		}
		return err
	}
	// Complete traces, newest first; a trace's first span (its root, by start time) names it, and the app's principalAttributes hook
	// named whom each span was for.
	query := telemetry.TraceQuery{Limit: new(10)}
	if name != "" {
		query.Name = &name
	}
	if principal != "" {
		query.Attributes = map[string]telemetry.AttributeValue{"chord.principal": principal}
	}
	found, err := store.FindTraces(query)
	if err != nil {
		return err
	}
	for _, trace := range found.Traces {
		start, end, failed := trace.Spans[0].StartedMs, trace.Spans[0].EndedMs, ""
		for _, span := range trace.Spans {
			start, end = min(start, span.StartedMs), max(end, span.EndedMs)
			if span.Status.Status == telemetry.SpanStatusCodeError {
				failed = "  error"
			}
		}
		_, _ = fmt.Fprintf(out, "%s  %s  %d spans  %s ms%s\n", trace.TraceID, trace.Spans[0].Name, len(trace.Spans), strconv.FormatFloat(end-start, 'f', 2, 64), failed)
	}
	// Metrics are stored per series and 10 s: a series is one set of attributes, here the direction.
	for _, direction := range []string{"sent", "received"} {
		rows, err := store.FindMetrics(telemetry.MetricQuery{Name: "chord.link.bytes", Attributes: map[string]telemetry.AttributeValue{"direction": direction}})
		if err != nil {
			return err
		}
		var bytes, messages float64
		for _, row := range rows {
			bytes, messages = bytes+row.Sum, messages+row.Count
		}
		_, _ = fmt.Fprintf(out, "chord.link.bytes %s: %d bytes in %d messages\n", direction, int64(bytes), int64(messages))
	}
	return nil
}

// printTelemetry prints telemetry indented, as the same text at every run: span ids, lengths of time and byte counts masked.
var (
	spanIDs   = regexp.MustCompile(`browser:[0-9a-f-]{36}:\d+|[0-9a-f-]{36}:\d+`)
	lengths   = regexp.MustCompile(` \(\+?[\d.]+ms\)`)
	durations = regexp.MustCompile(`duration=[\d.]+`)
	spent     = regexp.MustCompile(` [\d.]+ ms`)
	counts    = regexp.MustCompile(`\d+ bytes in \d+ messages`)
)

func (s *session) printTelemetry(text string) {
	text = spanIDs.ReplaceAllString(text, "<id>")
	text = lengths.ReplaceAllString(text, "")
	text = durations.ReplaceAllString(text, "duration=<ms>")
	text = spent.ReplaceAllString(text, " <ms>")
	text = counts.ReplaceAllString(text, "<n> bytes in <n> messages")
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		_, _ = fmt.Fprintln(s.out, "  ", line)
	}
}

// written is the newest count traces whose first span is name, once each has a span named last (the worker's spans are flushed).
func (s *session) written(name string, count int, last string) ([]telemetry.StoredTrace, error) {
	var complete []telemetry.StoredTrace
	err := until(func() (bool, error) {
		if err := s.store.Flush(); err != nil {
			return false, err
		}
		found, err := s.store.FindTraces(telemetry.TraceQuery{Name: &name, Limit: &count})
		if err != nil {
			return false, err
		}
		complete = complete[:0]
		for _, trace := range found.Traces {
			if slices.ContainsFunc(trace.Spans, func(span telemetry.StoredSpan) bool { return span.Name == last }) {
				complete = append(complete, trace)
			}
		}
		return len(complete) == count, nil
	})
	return complete, err
}

// telemetryStep is chapter XI. The server records to the store in the data directory (step 1 opened it) and writes what every list's
// worker sends it. A command-line client adds honey and a blank todo: each request is a `chord.http` trace of the server, the call
// into the list's worker under it, and the store's `add` checks the text in a span of its own (`ctx.Trace`). A page's click,
// recorded by the page itself, is the screenshots' (they need Chromium).
func (s *session) telemetryStep() error {
	for _, text := range []string{"honey", " "} {
		body, _ := json.Marshal(text)
		headers := s.cli()
		headers["Content-Type"] = "application/json"
		if _, _, err := s.request(http.MethodPost, "/api/lists/groceries", body, headers); err != nil {
			return err
		}
	}
	// The worker sends every second, the server writes every second: read until the worker's spans of both are there.
	traces, err := s.written("chord.http", 2, "todos.validate")
	if err != nil {
		return err
	}
	refused, honey := traces[0], traces[1]
	_, _ = fmt.Fprintln(s.out, "11. adding honey from the command line:")
	s.printTelemetry(telemetry.RenderTrace(honey.Spans))
	// `trace` reads the same file from a process of its own: the newest traces, then one by its id.
	read := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := traceCommand(args, &out)
		return out.String(), err
	}
	_, _ = fmt.Fprintln(s.out, "   trace <data directory> --name chord.http --principal owner:")
	listed, err := read(s.dataDir, "--name", "chord.http", "--principal", "owner")
	if err != nil {
		return err
	}
	s.printTelemetry(listed)
	_, _ = fmt.Fprintln(s.out, "   trace <data directory> <the refused one's id>:")
	one, err := read(s.dataDir, refused.TraceID)
	if err != nil {
		return err
	}
	s.printTelemetry(one)
	return nil
}
