### Changed

- Extension `setModel` takes a model object in every SDK, as Pi's `pi.setModel(model)` does. The session uses the object the extension passes, with the members it left on it (context window, input limits, base URL, API, compat, headers) and the credentials of its provider. PiG used to look the model up in its registry by provider and id and drop everything else. A registry model that an extension edits (Pi issue 9631: a `before_agent_start` handler that sets input limits) and a model built by hand for a provider PiG knows now work as under Pi. The session file still records the provider and the id only. Go: `Context.SetModel` takes the object (`map[string]any`) and the new `Context.SetModelNamed` takes the `provider/id` string. Python `set_model`, Rust `set_model` and Lisp `ctx-set-model` take the object or the string, and the string answers what it answered before.
- A model object an extension set stays the session's model until the registry's entry for its provider and id changes (the provider is registered again, `models.json` changes, a catalog refresh brings a new record). Then the session takes the registry's model.

### Upgrading

- Rebuild extensions that were built with an SDK before 0.8.0. The extension wire has one shape and no negotiation: a `setModel` that sends the old `provider/id` string is refused with `setModel: model must be a model object`.
- Go: `ctx.SetModel("provider/id")` becomes `ctx.SetModelNamed("provider/id")`. `pig extension upgrade` rewrites every `Context.SetModel` call whose argument is a string and lists, with its line, a call whose argument has another type. `ctx.SetModel(model)` takes a model object, for example one from `ctx.ModelRegistry().Find(provider, id)`.
- Python, Rust, Lisp: no change is needed. A `provider/id` string still works, and the call also takes a model object.
- Node: the call is unchanged, but the object is now used as passed. An extension that selected a registry model with an object carrying only `provider` and `id` must pass the registry's model: `pi.setModel(ctx.modelRegistry.find(provider, id))`.

### Fixed

- An extension's `setModel` of a model id that a registered provider object does not list answered `model not found` in every SDK. It now switches to the model, as it does for every other kind of provider and as Pi does. <!-- fix: d5e4cbc53f -->
- A Node extension's `pi.setModel(model)` with an edited or hand-built model object lost every member of the object but the provider and the id: the session took the registry's model, or one with no context window. <!-- fix: 0392315da8, 66df6c8115 -->