// Ports packages/coding-agent/src/experimental/client-tui.ts.
package experimental

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
)

// ClientTuiServer identifies a server and its independently scoped service sources. Radius enables connection recovery presentation.
type ClientTuiServer struct {
	ServerId string
	Radius   bool
	Server   ServerServiceSource
	Session  SessionServiceSource
}

// ExperimentalClientTuiOptions supplies presentation services and one UI owner executor. RunOnMain completes a top-level turn after its FIFO QueueMicrotask checkpoint; owner reentry executes inline without draining. Create and Close run off-loop; Render and HandleInput run on-loop. Keep the executor alive until Close completes.
type ExperimentalClientTuiOptions struct {
	Command        ClientCommand
	UI             tui.Renderer
	Servers        []ClientTuiServer
	FacetLoader    chord.FacetLoader
	RequestRender  func()
	Finish         func()
	RunOnMain      func(context.Context, func()) error
	QueueMicrotask func(context.Context, func()) error
}

// RunClientTuiOptions selects discovery and optional shared presentation facets.
type RunClientTuiOptions struct {
	OpenClientRuntimeOptions
	FacetLoader chord.FacetLoader
	// ThemePaths are the resolved theme resources of a DefaultResourceLoader without extensions, skills, prompt templates or context files, in precedence order.
	ThemePaths []string
}

type preparedClientSession struct {
	server              ClientTuiServer
	summary             services.SessionSummary
	presentationPlugins chord.JsonValue
}

type clientSessionFeature struct {
	server     ClientTuiServer
	directory  services.SessionDirectory
	management services.SessionManagement
	plugins    services.PresentationPlugins
}

type clientSessionSelection struct {
	feature *clientSessionFeature
	summary services.SessionSummary
}

func prepareClientSession(_ context.Context, command ClientCommand, servers []ClientTuiServer) (preparedClientSession, error) {
	// The upstream constructor has no caller Context; every service operation explicitly uses BACKGROUND_CONTEXT.
	ctx := context.Background()
	opened := make([]chord.RemoteServices, 0, len(servers))
	defer func() {
		// Upstream uses Promise.allSettled in finally and intentionally ignores disposal failures.
		var group sync.WaitGroup
		for _, scope := range opened {
			group.Go(func() { _ = scope.Dispose(context.Background()) })
		}
		group.Wait()
	}()
	for _, server := range servers {
		scope, err := server.Server.Open(chord.RemoteServiceSourceOpenOptions{Services: []string{
			services.SessionDirectoryDefinition.Id(), services.SessionManagementDefinition.Id(), services.PresentationPluginsDefinition.Id(),
		}})
		if err != nil {
			return preparedClientSession{}, err
		}
		opened = append(opened, scope)
	}
	features := make([]clientSessionFeature, len(servers))
	for i, scope := range opened {
		directory, err := chord.UseRemoteClient(scope, services.SessionDirectoryDefinition)
		if err != nil {
			return preparedClientSession{}, err
		}
		management, err := chord.UseRemoteClient(scope, services.SessionManagementDefinition)
		if err != nil {
			return preparedClientSession{}, err
		}
		plugins, err := chord.UseRemoteClient(scope, services.PresentationPluginsDefinition)
		if err != nil {
			return preparedClientSession{}, err
		}
		features[i] = clientSessionFeature{server: servers[i], directory: directory, management: management, plugins: plugins}
	}
	var group sync.WaitGroup
	var first sync.Once
	var readyError error
	for _, scope := range opened {
		group.Go(func() {
			if err := scope.Ready(ctx); err != nil {
				first.Do(func() { readyError = err })
			}
		})
	}
	group.Wait()
	if readyError != nil {
		return preparedClientSession{}, readyError
	}
	selected, err := selectClientTuiSession(ctx, command, features)
	if err != nil {
		return preparedClientSession{}, err
	}
	var paths []string
	if command.PluginPackages != nil {
		paths = make([]string, len(command.PluginPackages))
		for i, path := range command.PluginPackages {
			paths[i], err = filepath.Abs(path)
			if err != nil {
				return preparedClientSession{}, err
			}
		}
	}
	plugins, err := selected.feature.plugins.PrepareSession(ctx, services.PrepareSessionPluginsRequest{SessionId: selected.summary.SessionId, PackagePaths: paths})
	if err != nil {
		return preparedClientSession{}, err
	}
	if err := selected.feature.management.Attach(ctx, selected.summary.SessionId); err != nil {
		return preparedClientSession{}, err
	}
	if err := selected.feature.server.Session.WhenAttached(ctx, selected.summary.SessionId); err != nil {
		return preparedClientSession{}, err
	}
	return preparedClientSession{server: selected.feature.server, summary: selected.summary, presentationPlugins: plugins}, nil
}

func selectClientTuiSession(ctx context.Context, command ClientCommand, features []clientSessionFeature) (clientSessionSelection, error) {
	var matches []clientSessionSelection
	for i := range features {
		if command.SessionId == nil && (command.Continue == nil || !*command.Continue) && (command.Resume == nil || !*command.Resume) {
			break
		}
		feature := &features[i]
		state := feature.directory.State().Value()
		if state == nil {
			continue
		}
		for _, summary := range state.Sessions {
			if command.SessionId == nil || summary.SessionId == *command.SessionId {
				matches = append(matches, clientSessionSelection{feature: feature, summary: summary})
			}
		}
	}
	if command.SessionId != nil {
		switch len(matches) {
		case 0:
			if command.Connect != nil && command.Connect.Transport == "radius" {
				return clientSessionSelection{}, fmt.Errorf("Remote server does not contain Session %s", *command.SessionId)
			}
		case 1:
			return matches[0], nil
		default:
			return clientSessionSelection{}, fmt.Errorf("Session %s is available from more than one server", *command.SessionId)
		}
	} else if (command.Continue != nil && *command.Continue) || (command.Resume != nil && *command.Resume) {
		collator := collate.New(language.Und)
		slices.SortStableFunc(matches, func(left, right clientSessionSelection) int {
			if order := cmp.Compare(right.summary.CreatedAt, left.summary.CreatedAt); order != 0 {
				return order
			}
			if order := collator.CompareString(left.summary.ServerId, right.summary.ServerId); order != 0 {
				return order
			}
			return collator.CompareString(left.summary.SessionId, right.summary.SessionId)
		})
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	if len(features) != 1 {
		return clientSessionSelection{}, errors.New("Starting a Session requires exactly one server")
	}
	summary, err := features[0].management.Create(ctx, services.SessionCreateOptions{Id: command.SessionId})
	if err != nil {
		return clientSessionSelection{}, err
	}
	return clientSessionSelection{feature: &features[0], summary: summary}, nil
}
