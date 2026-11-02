### Upgrading

- Go extension SDK: `HostHelpers.HighlightCode` loses its `markdown` parameter, `func(code, lang string, palette []byte) ([]string, error)`, because `getMarkdownTheme().highlightCode` is `highlightCode`. Before: `func(code, lang string, markdown bool, palette []byte) ([]string, error)`. Only a fused host that installs its own helpers with `SetHostHelpers` sets this field; delete the `markdown` parameter and answer with the same lines for both. `pig extension upgrade` names the change.
