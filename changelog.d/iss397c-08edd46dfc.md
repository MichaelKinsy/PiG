### Fixed

- A terminal that went away while an extension's remote editor, an extension title, a clipboard copy (OSC 52), the external-editor notice or the startup model-scope notice wrote to it now ends the session with exit 129 like Pi, instead of the write error being discarded and the session carrying on (#397). <!-- fix: 08edd46dfc, 3cc0c71d33 -->
