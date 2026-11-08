// Package logs is p2's logs plugin (the server facet of apps/p2/plugins/logs/server.ts): the owner's process logs and
// notices, and the import of the detached hosts' published snapshots.
package logs

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/logs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/logs/server.ts.

const (
	maxSnapshotBytes = 2 * 1024 * 1024
	maxSnapshotLines = 1000
	collectEvery     = 300 * time.Millisecond
)

var errorLine = regexp.MustCompile(`^(?:\w*Error):`)

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func lineJSON(line p2runtime.LogLine) *delta.JsonObject {
	return delta.JsonObjectOf("id", float64(line.ID), "time", float64(line.Time), "level", string(line.Level), "text", line.Text)
}

func linesJSON(lines []p2runtime.LogLine) []any {
	out := make([]any, len(lines))
	for index, line := range lines {
		out[index] = lineJSON(line)
	}
	return out
}

func sourcesJSON(sources []p2runtime.LogSource) []any {
	out := make([]any, len(sources))
	for index, source := range sources {
		out[index] = delta.JsonObjectOf("id", source.ID, "lines", float64(source.Lines), "updatedAt", float64(source.UpdatedAt))
	}
	return out
}

func noticesJSON(notices []p2runtime.LogNotice) []any {
	out := make([]any, len(notices))
	for index, notice := range notices {
		out[index] = delta.JsonObjectOf("id", float64(notice.ID), "time", float64(notice.Time), "source", notice.Source, "message", notice.Message)
	}
	return out
}

// snapshotOf is the published snapshot of source in content, or false when it is not one (`isSnapshot`: an object with
// this `source` and at most 1,000 lines of the log line schema; other fields are allowed).
func snapshotOf(content []byte, source string) ([]p2runtime.LogLine, bool) {
	parsed, err := chordjson.Decode(content)
	snapshot := objectOf(parsed)
	if err != nil || snapshot == nil || snapshot.Value("source") != source {
		return nil, false
	}
	list, ok := snapshot.Value("lines").([]any)
	if !ok || len(list) > maxSnapshotLines {
		return nil, false
	}
	lines := make([]p2runtime.LogLine, len(list))
	for index, each := range list {
		line := objectOf(each)
		if line == nil {
			return nil, false
		}
		id, okID := line.Value("id").(float64)
		at, okTime := line.Value("time").(float64)
		level, okLevel := line.Value("level").(string)
		text, okText := line.Value("text").(string)
		if !okID || !okTime || !okLevel || !okText || (level != "stdout" && level != "stderr" && level != "event" && level != "error") {
			return nil, false
		}
		lines[index] = p2runtime.LogLine{ID: int(id), Time: int64(at), Level: p2runtime.LogLevel(level), Text: text}
	}
	return lines, true
}

// collector imports the detached hosts' published snapshots into the process logs, once per change.
type collector struct {
	directory    string
	mu           sync.Mutex
	active       bool
	fingerprints map[string]string
	seen         map[string]map[string]bool
}

func (c *collector) collect() {
	c.mu.Lock()
	if c.active {
		c.mu.Unlock()
		return
	}
	c.active = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active = false
		c.mu.Unlock()
	}()
	for _, source := range []string{"terminals", "dev"} {
		path := filepath.Join(c.directory, source+".logs.json")
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxSnapshotBytes {
			continue
		}
		fingerprint := strconv.FormatUint(inodeOf(info), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
		if c.fingerprints[source] == fingerprint {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines, ok := snapshotOf(content, source)
		if !ok {
			continue
		}
		prior := c.seen[source]
		next := map[string]bool{}
		for _, line := range lines {
			key := strconv.Itoa(line.ID) + ":" + strconv.FormatInt(line.Time, 10)
			next[key] = true
			if !prior[key] {
				p2runtime.ProcessLogs.AppendAt(source, line.Level, line.Text, line.Time)
				if line.Level == p2runtime.LogError && errorLine.MatchString(line.Text) {
					p2runtime.ProcessLogs.Notice(source, line.Text)
				}
			}
		}
		c.seen[source] = next
		c.fingerprints[source] = fingerprint
	}
}

func serveLogHost(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.log-host", func(self host.StoreSelf) error {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		self.Open(func(context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			directory, _ := objectOf(info).Value("dataDir").(string)
			p2runtime.ProcessLogs.Redactor.AddDirectory(directory)
			stop := p2runtime.CaptureProcessLogs("server", false)
			p2runtime.ProcessLogs.Append("server", p2runtime.LogEvent, "Server logs connected")
			// Detached processes publish private, bounded, redacted snapshots, not raw append-only files.
			imports := &collector{directory: directory, fingerprints: map[string]string{}, seen: map[string]map[string]bool{}}
			imports.collect()
			ticker := time.NewTicker(collectEvery)
			stopped := make(chan struct{})
			finished := make(chan struct{})
			//portlint:allow golifetime the collector ends when the host's native closes (stopped)
			go func() {
				defer close(finished)
				for {
					select {
					case <-ticker.C:
						imports.collect()
					case <-stopped:
						return
					}
				}
			}()
			return func() {
				ticker.Stop()
				close(stopped)
				<-finished
				stop()
			}, nil
		}, func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(func())()
			return nil
		})
		return nil
	})
}

func serveNotices(facet host.FacetHost) error {
	return facet.Serve(p2runtime.LogNotices, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("Notices are only available to owners")
		}
		// The process's notices as it keeps them: a dismissal tells this source before `dismiss` returns.
		notices := p2runtime.FollowNative(self,
			func(changed func()) func() { return p2runtime.ProcessLogs.Subscribe(func(string) { changed() }) },
			func() (delta.JsonValue, error) { return noticesJSON(p2runtime.ProcessLogs.Notices()), nil },
		)
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			// Diffed: the process tells of every log line, and the notices rarely change with one.
			State: map[string]chord.Watchable[delta.JsonValue]{"notices": self.Derive([]any{notices}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
			Methods: map[string]contracts.MethodFunction{
				"dismiss": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					id, _ := objectOf(args).Value("id").(float64)
					p2runtime.ProcessLogs.DismissNotice(int(id))
					return nil, nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveLogs(facet host.FacetHost) error {
	return facet.Serve(contract.Logs, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("Logs are only available to owners")
		}
		source, _ := params.Value("source").(string)
		sources := p2runtime.FollowNative(self,
			func(changed func()) func() { return p2runtime.ProcessLogs.Subscribe(func(string) { changed() }) },
			func() (delta.JsonValue, error) { return sourcesJSON(p2runtime.ProcessLogs.Sources()), nil },
		)
		// Read again only when this source changed (or went), not for every line of another one.
		lines := p2runtime.FollowNative(self,
			func(changed func()) func() {
				return p2runtime.ProcessLogs.Subscribe(func(changedSource string) {
					if changedSource == source || !hasSource(source) {
						changed()
					}
				})
			},
			func() (delta.JsonValue, error) { return linesJSON(p2runtime.ProcessLogs.Lines(source)), nil },
		)
		same := func(values []any) (delta.JsonValue, error) { return values[0], nil }
		// Diffed: a frame carries the lines and sources that changed, not the whole buffer.
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
			"sources": self.Derive([]any{sources}, same),
			"lines":   self.Derive([]any{lines}, same),
		}}}, nil
	}), host.ServeOptions{})
}

func hasSource(id string) bool {
	for _, source := range p2runtime.ProcessLogs.Sources() {
		if source.ID == id {
			return true
		}
	}
	return false
}

// Server is the plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveLogHost, serveNotices, serveLogs} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
