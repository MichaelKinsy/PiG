### Fixed

- RPC mode loads extensions before it resolves `--model` and `--models`, so a model registered by a `-e` extension, including a temporary Git source, can start an RPC session. `--mode rpc --list-models` lists extension-registered providers, and each model scope warning prints once.
- `--help` and `--list-models` print and exit 0 in every mode when an extension fails to load, as Pi does. Other startup still reports the extension error and exits 1.
