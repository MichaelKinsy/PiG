### Changed

- The `pig` binary is about 0.16 MB smaller: the embedded QuickJS module of `codemode` is stored as a zstd frame (260 KB instead of 287 KB), and the fuzzy filter of the selectors and the autocomplete (`FuzzyFilter`) and the keybinding conflict list no longer compile a copy of the sort for each element type they sort. The order of every list is the same.
