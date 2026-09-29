package ai

import (
	"testing"
)

func TestModelsRefreshCannotStartAReplacedProviderSnapshot(t *testing.T) {
	create := func() *ModelsProvider {
		return CreateProvider(CreateProviderOptions{ID: "dynamic", Auth: configuredTestAuth(), API: recordingProviderStreams("a", nil), FetchModels: func(RefreshModelsContext) ([]*Model, error) {
			return []*Model{dispatchTestModel("api-a", "model")}, nil
		}})
	}
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	old := create()
	models.SetProvider(old)
	snapshot := models.GetProviders()[0]
	replacement := create()
	models.SetProvider(replacement)
	if _, operation := models.beginProviderRefresh(t.Context(), snapshot); operation != nil {
		t.Fatal("replaced snapshot started new refresh work")
	}
	if models.GetProvider("dynamic") != replacement {
		t.Fatal("replacement was lost")
	}
}
