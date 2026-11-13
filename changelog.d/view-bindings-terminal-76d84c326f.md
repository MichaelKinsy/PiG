### Added

- In the terminal, the keys an extension binds on a view work while that view holds the focus: F6 enters the view, even when nothing in it takes keys, and a bound key the focused list or input does not use raises the extension's intent with the binding's argument. A binding is never global, and one row under the focused view names the bound keys that have a name. Gestures and keys bound on a widget view are not offered in the terminal; the extension is told once.
