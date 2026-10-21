package sdk

// ProjectTrustStoreEntry is Pi's ProjectTrustStoreEntry: the nearest saved decision for a directory.
type ProjectTrustStoreEntry struct {
	Path     string `json:"path"`
	Decision bool   `json:"decision"`
}

// ProjectTrustUpdate is Pi's ProjectTrustUpdate. A nil Decision is Pi's null: it removes the saved decision of Path.
type ProjectTrustUpdate struct {
	Path     string `json:"path"`
	Decision *bool  `json:"decision"`
}

// ProjectTrustStore is Pi's `ProjectTrustStore` class (core/trust-manager.ts): the project trust decisions saved in <agentDir>/trust.json. The host owns the store, so a decision saved here is the one the project trust gate reads. A decision is Pi's ProjectTrustDecision: true, false, or nil for undecided.
type ProjectTrustStore struct{ object *graphObject }

// NewProjectTrustStore is `new ProjectTrustStore(agentDir)`.
func NewProjectTrustStore(c Context, agentDir string) (ProjectTrustStore, error) {
	object, err := newGraphObject(c, "ProjectTrustStore", "new", agentDir)
	return ProjectTrustStore{object: object}, err
}

// Handle is the host's name for the store.
func (s ProjectTrustStore) Handle() string { return s.object.Handle() }

// Get is the nearest saved decision for cwd or one of its parents, nil when there is none.
func (s ProjectTrustStore) Get(cwd string) (*bool, error) {
	return graphMethodCall[*bool](s.object, "get", cwd)
}

// GetEntry is the nearest saved decision with the directory that holds it, nil when there is none.
func (s ProjectTrustStore) GetEntry(cwd string) (*ProjectTrustStoreEntry, error) {
	return graphMethodCall[*ProjectTrustStoreEntry](s.object, "getEntry", cwd)
}

// Set saves the decision for cwd; a nil decision removes it.
func (s ProjectTrustStore) Set(cwd string, decision *bool) error {
	_, err := graphMethodCall[graphDone](s.object, "set", cwd, decision)
	return err
}

// SetMany applies several decisions in one locked update of the store.
func (s ProjectTrustStore) SetMany(decisions []ProjectTrustUpdate) error {
	_, err := graphMethodCall[graphDone](s.object, "setMany", decisions)
	return err
}

// HasTrustRequiringProjectResources is Pi's hasTrustRequiringProjectResources(cwd): whether cwd holds project-local resources that project trust gates (entries of its configuration directory, or .agents/skills in cwd or an ancestor other than the user's).
func HasTrustRequiringProjectResources(c Context, cwd string) (bool, error) {
	return graphStatic[bool](c, "ProjectTrustStore", "hasTrustRequiringProjectResources", cwd)
}
