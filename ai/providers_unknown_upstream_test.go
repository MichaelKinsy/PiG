package ai

import "testing"

// .upstream/v0.99.2/packages/ai/test/providers.test.ts:79 "returns empty results for unknown provider ids".
// The typed reads map to Go as: getBuiltinModel/getCompatModel -> LookupModelExact (catalog registry),
// getBuiltinModels/getCompatModels -> ListModels, getBuiltinImageModel(s) -> GetImageModel/GetImageModels,
// getBuiltinClassifierModel(s) -> GetBuiltinClassifierModel/GetBuiltinClassifierModels, getAllBuiltinModels -> GetAllBuiltinModels.
func TestBuiltinProvidersUnknownProviderIDsUpstream(t *testing.T) {
	const unknownProvider, unknownModel = "not-a-provider", "x"
	if model, ok := LookupModelExact(unknownProvider + "/" + unknownModel); ok || model != nil {
		t.Errorf("LookupModelExact = %#v, %v; want nothing", model, ok)
	}
	if image, ok := GetImageModel(unknownProvider, unknownModel); ok {
		t.Errorf("GetImageModel = %#v; want nothing", image)
	}
	if classifier := GetBuiltinClassifierModel(unknownProvider, unknownModel); classifier != nil {
		t.Errorf("GetBuiltinClassifierModel = %#v; want nil", classifier)
	}
	if models := ListModels(unknownProvider); len(models) != 0 {
		t.Errorf("ListModels = %d models; want none", len(models))
	}
	if images := GetImageModels(unknownProvider); len(images) != 0 {
		t.Errorf("GetImageModels = %d models; want none", len(images))
	}
	if classifiers := GetBuiltinClassifierModels(unknownProvider); len(classifiers) != 0 {
		t.Errorf("GetBuiltinClassifierModels = %d models; want none", len(classifiers))
	}
	if all := GetAllBuiltinModels(unknownProvider); len(all) != 0 {
		t.Errorf("GetAllBuiltinModels = %d models; want none", len(all))
	}
}
