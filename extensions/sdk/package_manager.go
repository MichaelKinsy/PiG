package sdk

// ResolvedResource is Pi's ResolvedResource: a resource path a package or setting contributes, with where it came from.
type ResolvedResource struct {
	Path     string       `json:"path"`
	Enabled  bool         `json:"enabled"`
	Metadata PathMetadata `json:"metadata"`
}

// ResolvedPaths is Pi's ResolvedPaths.
type ResolvedPaths struct {
	Extensions []ResolvedResource `json:"extensions"`
	Skills     []ResolvedResource `json:"skills"`
	Prompts    []ResolvedResource `json:"prompts"`
	Themes     []ResolvedResource `json:"themes"`
}

// ConfiguredPackage is Pi's ConfiguredPackage: a package named in the user or project settings. Scope is "user" or "project".
type ConfiguredPackage struct {
	Source        string  `json:"source"`
	Scope         string  `json:"scope"`
	Filtered      bool    `json:"filtered"`
	InstalledPath *string `json:"installedPath,omitempty"`
}

// PackageUpdate is Pi's PackageUpdate: a configured package with a newer version available. Type is "npm" or "git".
type PackageUpdate struct {
	Source      string `json:"source"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
}

// ProgressEvent is Pi's ProgressEvent of an install, remove or update.
type ProgressEvent struct {
	Type    string  `json:"type"`
	Action  string  `json:"action"`
	Source  string  `json:"source"`
	Message *string `json:"message,omitempty"`
}

// MissingSourceAction is what a resolve callback decides for a package that is not installed: "install", "skip" or "error".
type MissingSourceAction string

// PackageManagerOptions is Pi's PackageManagerOptions. BuiltinExtensions are the names of built-in extensions resolved as `builtin:<name>`.
type PackageManagerOptions struct {
	Cwd               string
	AgentDir          string
	SettingsManager   SettingsManager
	BuiltinExtensions []string
}

// DefaultPackageManager is Pi's `DefaultPackageManager` class (core/package-manager.ts): it installs, removes, resolves and lists the packages named in the user and project settings of the SettingsManager it was given. The host owns the manager.
type DefaultPackageManager struct{ object *graphObject }

// NewDefaultPackageManager is `new DefaultPackageManager(options)`.
func NewDefaultPackageManager(c Context, options PackageManagerOptions) (*DefaultPackageManager, error) {
	object, err := newGraphObject(c, "DefaultPackageManager", "new", map[string]any{
		"cwd": options.Cwd, "agentDir": options.AgentDir, "settingsManager": options.SettingsManager.Handle(), "builtinExtensions": options.BuiltinExtensions,
	})
	if err != nil {
		return nil, err
	}
	return &DefaultPackageManager{object: object}, nil
}

// Handle is the host's name for the manager.
func (m *DefaultPackageManager) Handle() string { return m.object.Handle() }

// SetProgressCallback sets the function every later install, remove and update reports to; nil clears it.
func (m *DefaultPackageManager) SetProgressCallback(callback func(ProgressEvent)) error {
	_, err := graphMethodCall[graphDone](m.object, "setProgressCallback", m.object.context.callbackID(graphFunc(func(event ProgressEvent) any {
		callback(event)
		return nil
	})))
	return err
}

func localOption(local bool) map[string]any { return map[string]any{"local": local} }

// AddSourceToSettings adds source to the user (or project, with local) settings; it reports whether the settings changed.
func (m *DefaultPackageManager) AddSourceToSettings(source string, local bool) (bool, error) {
	return graphMethodCall[bool](m.object, "addSourceToSettings", source, localOption(local))
}

// RemoveSourceFromSettings removes source from the user (or project, with local) settings; it reports whether the settings changed.
func (m *DefaultPackageManager) RemoveSourceFromSettings(source string, local bool) (bool, error) {
	return graphMethodCall[bool](m.object, "removeSourceFromSettings", source, localOption(local))
}

// GetInstalledPath is where the package source is installed in scope ("user" or "project"), nil when it is not.
func (m *DefaultPackageManager) GetInstalledPath(source, scope string) (*string, error) {
	return graphMethodCall[*string](m.object, "getInstalledPath", source, scope)
}

// Resolve resolves every configured package into the resources it contributes. onMissing, when not nil, decides for each package that is not installed.
func (m *DefaultPackageManager) Resolve(onMissing func(source string) MissingSourceAction) (ResolvedPaths, error) {
	return graphMethodAsync[ResolvedPaths](m.object, "resolve", m.object.context.callbackID(graphFunc(func(source string) any {
		if onMissing == nil {
			return MissingSourceAction("install")
		}
		return onMissing(source)
	})))
}

// ResolveExtensionSources resolves sources (`-e` arguments) into the resources they contribute, in the scope "temporary", "project" (local) or "user".
func (m *DefaultPackageManager) ResolveExtensionSources(sources []string, local, temporary bool) (ResolvedPaths, error) {
	return graphMethodAsync[ResolvedPaths](m.object, "resolveExtensionSources", sources, map[string]any{"local": local, "temporary": temporary})
}

// ListConfiguredPackages is the user then project packages of the settings, with their installed paths.
func (m *DefaultPackageManager) ListConfiguredPackages() ([]ConfiguredPackage, error) {
	return graphMethodCall[[]ConfiguredPackage](m.object, "listConfiguredPackages")
}

// Install installs source without changing the settings.
func (m *DefaultPackageManager) Install(source string, local bool) error {
	_, err := graphMethodAsync[graphDone](m.object, "install", source, localOption(local))
	return err
}

// InstallAndPersist installs source and adds it to the settings.
func (m *DefaultPackageManager) InstallAndPersist(source string, local bool) error {
	_, err := graphMethodAsync[graphDone](m.object, "installAndPersist", source, localOption(local))
	return err
}

// Remove removes the installation of source without changing the settings.
func (m *DefaultPackageManager) Remove(source string, local bool) error {
	_, err := graphMethodAsync[graphDone](m.object, "remove", source, localOption(local))
	return err
}

// RemoveAndPersist removes source and drops it from the settings; it reports whether the settings changed.
func (m *DefaultPackageManager) RemoveAndPersist(source string, local bool) (bool, error) {
	return graphMethodAsync[bool](m.object, "removeAndPersist", source, localOption(local))
}

// Update updates source, or every unpinned configured package when source is nil.
func (m *DefaultPackageManager) Update(source *string) error {
	_, err := graphMethodAsync[graphDone](m.object, "update", source)
	return err
}

// CheckForAvailableUpdates lists the configured packages with a newer version available.
func (m *DefaultPackageManager) CheckForAvailableUpdates() ([]PackageUpdate, error) {
	return graphMethodAsync[[]PackageUpdate](m.object, "checkForAvailableUpdates")
}
