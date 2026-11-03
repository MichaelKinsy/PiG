### Fixed

- A terminal that went away while an extension's remote editor, an extension terminal write, the window title, a clipboard copy or the cursor and renderer output wrote to it now ends the session with exit 129 like Pi, instead of the write error being discarded and the session carrying on (#397). <!-- fix: 08edd46dfc -->
