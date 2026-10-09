### Fixed

- The external editor (`ctrl+g`) now edits `prompt.md` inside a private `pi-editor-*` directory under the system temp directory and removes the directory afterwards, as Pi does. It previously wrote a `pig-editor-*.md` file directly in the shared temp directory.
