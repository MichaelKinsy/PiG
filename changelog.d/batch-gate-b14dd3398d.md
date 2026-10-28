### Upgrading

- Go API `coding/extension/host/subprocess`: an extension's `TuiAltScreen` needs `subprocess.EnableTuiAltScreens()`, which the `pig` command calls where it creates the extension host. A program that serves extension screens to its own terminal calls it once before the first extension loads; until then a `tui.alt` call fails with an error that names the call. A program that serves none, such as `examples/headless`, needs no change and does not link the alternate-screen renderer or the word-segmentation dictionaries (6.3 MB) behind its text inputs.
