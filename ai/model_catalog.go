package ai

// Ports packages/ai/src/model-catalog.ts: ChatModelCatalog, ImageModelCatalog and ClassifierModelCatalog (the record
// that flattenChatModelCatalog, flattenImageModelCatalog and flattenClassifierModelCatalog build from a provider's
// providers/data/<id>.json file).

// orderedCatalog is a record of models keyed by model id. It keeps insertion order because upstream reads it with
// Object.values and Object.keys.
type orderedCatalog[M any] struct {
	ids    []string
	models map[string]M
}

func newOrderedCatalog[M any](capacity int) orderedCatalog[M] {
	return orderedCatalog[M]{ids: make([]string, 0, capacity), models: make(map[string]M, capacity)}
}

// add sets a model. An id that is already present keeps its position and takes the new model, as a JavaScript object does.
func (c *orderedCatalog[M]) add(id string, model M) {
	if _, ok := c.models[id]; !ok {
		c.ids = append(c.ids, id)
	}
	c.models[id] = model
}

// Get returns the model with the given id (catalog[id]).
func (c orderedCatalog[M]) Get(id string) (M, bool) {
	model, ok := c.models[id]
	return model, ok
}

// IDs returns the model ids in catalog order (Object.keys).
func (c orderedCatalog[M]) IDs() []string { return append([]string(nil), c.ids...) }

// Values returns the models in catalog order (Object.values).
func (c orderedCatalog[M]) Values() []M {
	values := make([]M, 0, len(c.ids))
	for _, id := range c.ids {
		values = append(values, c.models[id])
	}
	return values
}

// Len returns the number of models.
func (c orderedCatalog[M]) Len() int { return len(c.ids) }

// ChatModelCatalog holds the chat models of one provider by model id.
type ChatModelCatalog struct{ orderedCatalog[*Model] }

// ImageModelCatalog holds the image models of one provider by model id.
type ImageModelCatalog struct{ orderedCatalog[*ImageModel] }

// ClassifierModelCatalog holds the classifier models of one provider by model id.
type ClassifierModelCatalog struct {
	orderedCatalog[*ClassifierModel]
}

func chatModelCatalog(provider string) ChatModelCatalog {
	models := ListModels(provider)
	catalog := ChatModelCatalog{newOrderedCatalog[*Model](len(models))}
	for i := range models {
		catalog.add(models[i].ID, models[i].ToModel())
	}
	return catalog
}

func imageModelCatalog(provider string) ImageModelCatalog {
	catalog := ImageModelCatalog{newOrderedCatalog[*ImageModel](0)}
	for i := range GeneratedImageModels {
		model := GeneratedImageModels[i]
		if model.Provider != provider {
			continue
		}
		model.Headers = cloneStringMap(model.Headers)
		model.Input = append([]string(nil), model.Input...)
		model.Output = append([]string(nil), model.Output...)
		catalog.add(model.ID, &model)
	}
	return catalog
}

func classifierModelCatalog(provider string) ClassifierModelCatalog {
	models := GetBuiltinClassifierModels(provider)
	catalog := ClassifierModelCatalog{newOrderedCatalog[*ClassifierModel](len(models))}
	for _, model := range models {
		catalog.add(model.ID, model)
	}
	return catalog
}
