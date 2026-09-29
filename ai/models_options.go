package ai

// Ports packages/ai/src/models.ts (CreateModelsOptions).

// CreateModelsOptions supplies application-owned stores and auth context to model collections.
type CreateModelsOptions struct {
	Credentials CredentialStore
	ModelsStore ModelsStore
	AuthContext *AuthContext
}
