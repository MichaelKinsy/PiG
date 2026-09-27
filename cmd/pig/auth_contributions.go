package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/fusepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type authContribution struct {
	ID             string
	Name           string
	CallbackServer bool
	Extension      string
}

type authInspectionDiagnostic struct {
	Extension string `json:"extension"`
	Error     string `json:"error"`
}

type duplicateAuthTargetError struct{ message string }

func (e *duplicateAuthTargetError) Error() string { return e.message }

type authContributionRegistry struct {
	cwd           string
	targets       map[string]authContribution
	diagnostics   []authInspectionDiagnostic
	owners        map[string]string
	configs       map[string]subprocess.ExtConfig
	embedded      map[string]struct{}
	embeddedCells []cellpack.LoadedCell
	loaded        map[string]struct{}
	host          *subprocess.Host
	ctx           context.Context
	cancel        context.CancelFunc
}

// pig additive (D40): pre-session OAuth inventory uses registration-only extension startup.
// discoverAuthContributions inspects runtime registration only. It starts no
// session runner and dispatches no lifecycle or capability handler.
func discoverAuthContributions() (*authContributionRegistry, error) {
	return discoverAuthContributionsFor("")
}

func discoverAuthContributionsFor(targetID string) (*authContributionRegistry, error) {
	cwd, agentDir, settings, err := packageContext()
	if err != nil {
		return nil, err
	}
	configs, packageDiagnostics, err := authExtensionConfigs(cwd, agentDir, settings)
	if err != nil {
		return nil, err
	}
	fusepack.Register()
	configs = normalizeRuntimeExtensionConfigs(configs)
	slices.SortFunc(configs, func(a, b subprocess.ExtConfig) int { return strings.Compare(a.Name, b.Name) })

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if len(configs) > 0 {
		// Inspection builds source extensions, so it needs the staged SDKs a
		// clean host has only after this first stage.
		stageExtensionSDKsAtStartup(ctx, codingagent.ConfigRoot(), os.Stderr, startupSDKLockTimeout)
	}
	registry := &authContributionRegistry{
		cwd: cwd, targets: make(map[string]authContribution), diagnostics: packageDiagnostics, owners: make(map[string]string),
		configs: make(map[string]subprocess.ExtConfig), embedded: make(map[string]struct{}), loaded: make(map[string]struct{}),
		ctx: ctx, cancel: cancel,
	}
	for _, provider := range ai.GetOAuthProviders() {
		registry.owners[provider.ID()] = "core"
	}
	for _, config := range configs {
		registry.configs[config.Name] = config
		if projected, projectionErr := registry.loadProjection(config); projectionErr != nil {
			registry.addDiagnostic(config.Name, projectionErr)
		} else if projected {
			continue
		}
		if err := registry.inspectConfigs([]subprocess.ExtConfig{config}); err != nil {
			registry.close()
			return nil, err
		}
		if err := registry.storeProjection(config); err != nil {
			registry.addDiagnostic(config.Name, err)
		}
	}

	if err := cellpack.Register(); err != nil {
		registry.close()
		return nil, fmt.Errorf("register Piglet Binary cells for authentication inspection: %w", err)
	}
	cells := cellpack.LoadedCells()
	registry.embeddedCells = cells
	if len(cells) > 0 {
		for _, cell := range cells {
			for _, extension := range cell.Extensions {
				registry.embedded[extension.Name] = struct{}{}
			}
		}
		if err := registry.inspectEmbedded(cells, targetID); err != nil {
			registry.close()
			return nil, err
		}
	}
	return registry, nil
}

// authExtensionConfigs keeps Package validation strict for every enabled
// non-extension resource, but turns an enabled extension's missing or invalid
// source into an inventory diagnostic. A broken Package extension therefore
// cannot erase healthy authentication targets.
func authExtensionConfigs(cwd, agentDir string, settings *codingagent.SettingsManager) ([]subprocess.ExtConfig, []authInspectionDiagnostic, error) {
	var configs []subprocess.ExtConfig
	var diagnostics []authInspectionDiagnostic
	for _, pkg := range configuredPackagesForResolution(cwd, settings) {
		if pkg.InstalledPath == "" {
			continue
		}
		filters, err := effectiveConfiguredPackageFilters(pkg)
		if err != nil {
			return nil, nil, fmt.Errorf("%s Package %q at %s is invalid: %w", pkg.Scope, pkg.Source.Source, pkg.InstalledPath, err)
		}
		inspectionFilters := make(map[packagecontent.Kind][]string, len(filters))
		for kind, patterns := range filters {
			inspectionFilters[kind] = slices.Clone(patterns)
		}
		inspectionFilters[packagecontent.Extensions] = []string{}
		resources, missing, err := packagecontent.InspectConfigured(pkg.InstalledPath, inspectionFilters)
		if err != nil {
			return nil, nil, fmt.Errorf("%s Package %q at %s is invalid: %w", pkg.Scope, pkg.Source.Source, pkg.InstalledPath, err)
		}
		for _, member := range missing {
			if member.Kind == packagecontent.Extensions {
				if packagecontent.ResourceEnabled(member.Pattern, filters[packagecontent.Extensions]) {
					diagnostics = append(diagnostics, authInspectionDiagnostic{Extension: member.Pattern, Error: fmt.Sprintf("extension source %s does not exist", member.Path)})
				}
				continue
			}
		}
		for _, source := range resources.ExtensionEntries {
			relative, _ := filepath.Rel(pkg.InstalledPath, source)
			if !packagecontent.ResourceEnabled(filepath.ToSlash(relative), filters[packagecontent.Extensions]) {
				continue
			}
			name, err := packagecontent.PublicName(packagecontent.Extensions, source, "")
			if err != nil {
				diagnostics = append(diagnostics, authInspectionDiagnostic{Extension: filepath.ToSlash(relative), Error: err.Error()})
				continue
			}
			config, _, err := subprocess.ResolveExtConfigWithIdentity(source, name)
			if err != nil {
				diagnostics = append(diagnostics, authInspectionDiagnostic{Extension: name, Error: err.Error()})
				continue
			}
			configs = append(configs, config)
		}
	}
	var topLevel []subprocess.ExtConfig
	topLevel = append(topLevel, collectTopLevelExtensionConfigs(filepath.Join(agentDir, "extensions"), settings.GetGlobalSettings().Extensions)...)
	if projectRoot, ok := projectResourceRoot(cwd); ok {
		topLevel = append(topLevel, collectTopLevelExtensionConfigs(filepath.Join(projectRoot, "extensions"), settings.GetProjectSettings().Extensions)...)
	}
	// A top-level extension whose source does not resolve is an inventory
	// diagnostic too, as upstream package-manager-cli.ts warns on each
	// extension load error.
	for _, config := range topLevel {
		if resolveErr := config.ResolveError(); resolveErr != nil {
			diagnostics = append(diagnostics, authInspectionDiagnostic{Extension: filepath.Base(config.Name), Error: resolveErr.Error()})
			continue
		}
		configs = append(configs, config)
	}
	return mergeExtConfigs(configs), diagnostics, nil
}

type authRegistrationProjection struct {
	ConfigDigest   string             `json:"configDigest"`
	ArtifactDigest string             `json:"artifactDigest"`
	Providers      []authContribution `json:"providers"`
}

const authRegistrationProjectionFile = "auth-registration.json"

func (r *authContributionRegistry) projectionEntry(config subprocess.ExtConfig) (string, string, string, bool) {
	cacheRoot := filepath.Join(codingagent.ConfigRoot(), "cache")
	entries, errs := subprocess.CurrentCacheEntries([]subprocess.ExtConfig{config}, cacheRoot)
	if len(errs) > 0 || len(entries) != 1 {
		return "", "", "", false
	}
	entry := ""
	for path := range entries {
		entry = path
	}
	artifactDigest, valid := runtimecell.ArtifactIdentity(entry)
	if !valid {
		return "", "", "", false
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", "", "", false
	}
	digest := sha256.Sum256(encoded)
	return entry, hex.EncodeToString(digest[:]), artifactDigest, true
}

func (r *authContributionRegistry) loadProjection(config subprocess.ExtConfig) (bool, error) {
	entry, configDigest, artifactDigest, ok := r.projectionEntry(config)
	if !ok {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(entry, authRegistrationProjectionFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var projection authRegistrationProjection
	if json.Unmarshal(data, &projection) != nil || projection.ConfigDigest != configDigest || projection.ArtifactDigest != artifactDigest {
		return false, nil
	}
	for _, provider := range projection.Providers {
		if provider.Extension != config.Name {
			return false, nil
		}
		if err := r.add(provider); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (r *authContributionRegistry) storeProjection(config subprocess.ExtConfig) error {
	entry, configDigest, artifactDigest, ok := r.projectionEntry(config)
	if !ok {
		return nil
	}
	providers := make([]authContribution, 0)
	for _, provider := range r.targets {
		if provider.Extension == config.Name {
			providers = append(providers, provider)
		}
	}
	slices.SortFunc(providers, func(a, b authContribution) int { return strings.Compare(a.ID, b.ID) })
	data, err := json.Marshal(authRegistrationProjection{ConfigDigest: configDigest, ArtifactDigest: artifactDigest, Providers: providers})
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(entry, ".auth-registration-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(entry, authRegistrationProjectionFile))
}

func (r *authContributionRegistry) inspectConfigs(configs []subprocess.ExtConfig) error {
	configs = slices.Clone(configs)
	slices.SortFunc(configs, func(a, b subprocess.ExtConfig) int { return strings.Compare(a.Name, b.Name) })
	for _, config := range configs {
		host := subprocess.NewHostWithConfigRoot(r.cwd, codingagent.ConfigRoot())
		host.SetMode("print")
		loaded, loadErrors := host.LoadAll(r.ctx, []subprocess.ExtConfig{config})
		for _, loadErr := range loadErrors {
			r.addDiagnostic(config.Name, loadErr)
		}
		for _, extension := range loaded {
			if err := r.captureRuntimeProviders(host, extension.Name); err != nil {
				host.Shutdown("authentication inspection failed")
				if _, ok := errors.AsType[*duplicateAuthTargetError](err); ok {
					return err
				}
				r.addDiagnostic(extension.Name, err)
			}
		}
		host.Shutdown("authentication inspection complete")
	}
	return nil
}

func (r *authContributionRegistry) addDiagnostic(extensionName string, err error) {
	if err == nil {
		return
	}
	r.diagnostics = append(r.diagnostics, authInspectionDiagnostic{Extension: extensionName, Error: err.Error()})
}
func (r *authContributionRegistry) inspectEmbedded(cells []cellpack.LoadedCell, targetID ...string) error {
	preferred := ""
	if len(targetID) > 0 {
		preferred = targetID[0]
	}
	members := make([]cellpack.LoadedCell, 0)
	for _, cell := range cells {
		for _, member := range cell.Extensions {
			memberCell := cell
			memberCell.Extensions = []cellpack.ExtEntry{member}
			if member.Name == preferred {
				members = append([]cellpack.LoadedCell{memberCell}, members...)
				continue
			}
			members = append(members, memberCell)
		}
	}
	for _, cell := range members {
		if preferred != "" {
			if _, found := r.target(preferred); found {
				break
			}
		}
		host := subprocess.NewHostWithConfigRoot(r.cwd, codingagent.ConfigRoot())
		host.SetMode("print")
		loaded, loadErrors := host.LoadEmbeddedCells(r.ctx, embeddedCellsFromCellpack([]cellpack.LoadedCell{cell}))
		for _, loadErr := range loadErrors {
			r.addDiagnostic(cell.Extensions[0].Name, loadErr)
		}
		for _, extension := range loaded {
			if err := r.captureRuntimeProviders(host, extension.Name); err != nil {
				host.Shutdown("authentication inspection failed")
				if _, ok := errors.AsType[*duplicateAuthTargetError](err); ok {
					return err
				}
				r.addDiagnostic(extension.Name, err)
			}
		}
		host.Shutdown("authentication inspection complete")
	}
	return nil
}
func (r *authContributionRegistry) captureRuntimeProviders(host *subprocess.Host, extensionName string) error {
	ids := host.OAuthProviderNames(extensionName)
	slices.Sort(ids)
	for _, id := range ids {
		provider, ok := ai.GetOAuthProvider(id)
		if !ok {
			return fmt.Errorf("extension %q registered OAuth provider %q without a host provider", extensionName, id)
		}
		if err := r.add(authContribution{
			ID: id, Name: provider.Name(), CallbackServer: provider.UsesCallbackServer(), Extension: extensionName,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *authContributionRegistry) add(target authContribution) error {
	if r.targets == nil {
		r.targets = make(map[string]authContribution)
	}
	if r.owners == nil {
		r.owners = make(map[string]string)
	}
	if previousOwner, exists := r.owners[target.ID]; exists && previousOwner != target.Extension {
		owners := []string{previousOwner, target.Extension}
		slices.Sort(owners)
		return &duplicateAuthTargetError{message: fmt.Sprintf("duplicate auth target %q from extensions %q and %q", target.ID, owners[0], owners[1])}
	}
	if previous, exists := r.targets[target.ID]; exists {
		if previous.Extension == target.Extension && previous.Name == target.Name && previous.CallbackServer == target.CallbackServer {
			return nil
		}
		return fmt.Errorf("extension %q registered conflicting OAuth provider %q metadata", target.Extension, target.ID)
	}
	r.owners[target.ID] = target.Extension
	r.targets[target.ID] = target
	return nil
}

func (r *authContributionRegistry) target(id string) (authContribution, bool) {
	target, ok := r.targets[id]
	return target, ok
}

func (r *authContributionRegistry) loadAll() error {
	ids := make([]string, 0, len(r.targets))
	for id := range r.targets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var errs []error
	for _, id := range ids {
		if _, err := r.load(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *authContributionRegistry) load(id string) (ai.OAuthProviderInterface, error) {
	if _, ok := r.target(id); !ok {
		if provider, builtIn := ai.GetOAuthProvider(id); builtIn {
			return provider, nil
		}
		return nil, fmt.Errorf("Unknown provider: %s", id)
	}
	target, _ := r.target(id)
	if _, ok := r.loaded[target.Extension]; !ok {
		if err := r.loadExtension(target.Extension); err != nil {
			return nil, fmt.Errorf("load auth target %q from extension %q: %w", id, target.Extension, err)
		}
		r.loaded[target.Extension] = struct{}{}
	}
	provider, ok := ai.GetOAuthProvider(id)
	if !ok {
		return nil, fmt.Errorf("extension %q inspected OAuth provider %q but did not register it on launch", target.Extension, id)
	}
	return provider, nil
}

func (r *authContributionRegistry) loadExtension(name string) error {
	host := r.extensionHost()
	if config, ok := r.configs[name]; ok {
		_, errs := host.LoadAll(r.ctx, []subprocess.ExtConfig{config})
		return errors.Join(errs...)
	}
	if _, ok := r.embedded[name]; !ok {
		return fmt.Errorf("no installed or embedded extension payload found")
	}
	selected := selectEmbeddedOwnerCells(r.embeddedCells, name)
	if len(selected) == 0 {
		return fmt.Errorf("embedded extension payload is missing")
	}
	_, errs := host.LoadEmbeddedCells(r.ctx, embeddedCellsFromCellpack(selected))
	return errors.Join(errs...)
}

func selectEmbeddedOwnerCells(cells []cellpack.LoadedCell, name string) []cellpack.LoadedCell {
	selected := make([]cellpack.LoadedCell, 0, 1)
	for _, cell := range cells {
		for _, extension := range cell.Extensions {
			if extension.Name != name {
				continue
			}
			ownerCell := cell
			ownerCell.Extensions = []cellpack.ExtEntry{extension}
			selected = append(selected, ownerCell)
			break
		}
	}
	return selected
}

func (r *authContributionRegistry) extensionHost() *subprocess.Host {
	if r.host == nil {
		r.host = subprocess.NewHostWithConfigRoot(r.cwd, codingagent.ConfigRoot())
		r.host.SetMode("print")
	}
	return r.host
}

func (r *authContributionRegistry) close() {
	if r.host != nil {
		r.host.Shutdown("authentication command complete")
	}
	if r.cancel != nil {
		r.cancel()
	}
}

func registeredAuthTargetItems(registry *authContributionRegistry) []authTargetItem {
	providers := sortedOAuthProviders()
	items := make([]authTargetItem, 0, len(providers))
	seen := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		items = append(items, authTargetItem{ID: provider.ID(), Name: provider.Name(), CallbackServer: provider.UsesCallbackServer()})
		seen[provider.ID()] = struct{}{}
	}
	if registry == nil {
		return items
	}
	var contributed []authTargetItem
	for id, target := range registry.targets {
		if _, coreWins := seen[id]; coreWins {
			continue
		}
		contributed = append(contributed, authTargetItem{ID: target.ID, Name: target.Name, CallbackServer: target.CallbackServer})
	}
	slices.SortFunc(contributed, func(a, b authTargetItem) int {
		if value := strings.Compare(a.Name, b.Name); value != 0 {
			return value
		}
		return strings.Compare(a.ID, b.ID)
	})
	return append(items, contributed...)
}

func authProviderFromContribution(id string, registry **authContributionRegistry) (ai.OAuthProviderInterface, error) {
	if provider, ok := ai.GetOAuthProvider(id); ok {
		return provider, nil
	}
	if *registry == nil {
		discovered, err := discoverAuthContributionsFor(id)
		if err != nil {
			return nil, err
		}
		*registry = discovered
	}
	return (*registry).load(id)
}

func loadDeclaredAuthProvider(id string, registry **authContributionRegistry) error {
	if _, ok := ai.GetOAuthProvider(id); ok {
		return nil
	}
	if *registry == nil {
		discovered, err := discoverAuthContributionsFor(id)
		if err != nil {
			return err
		}
		*registry = discovered
	}
	if _, declared := (*registry).target(id); !declared {
		return nil
	}
	_, err := (*registry).load(id)
	return err
}

func closeAuthContributionRegistry(registry *authContributionRegistry) {
	if registry != nil {
		registry.close()
	}
}
