package ai

// Ports packages/ai/src/models.ts (CreateModelsOptions).

// CreateModelsOptions supplies application-owned stores and auth context to model collections.
type CreateModelsOptions struct {
	Credentials CredentialStore
	ModelsStore ModelsStore
	AuthContext *AuthContext
	// Identity is the default identity of every request; a request's Identity overrides it field by field.
	Identity *ClientIdentity
}
