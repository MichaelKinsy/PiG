### Upgrading

- extensions (Go embedders): a program that embeds the extension host must call `subprocess.EnableViewWidgets()` (and `subprocess.EnableEditorViews()` for the editor) before it builds a view, or a view that holds one of those kinds is rejected with a message that names the call. The `pig` command is unchanged: it makes both calls before it runs a command.
