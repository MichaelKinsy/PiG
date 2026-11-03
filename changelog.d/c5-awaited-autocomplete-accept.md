### Fixed

- Accepting an autocomplete popup of awaited command-argument completions after the cursor moved applies the popup at the cursor as Pi does, instead of dropping it, and a cursor placed before the start of the popup's prefix splices the completion as Pi's string slice does.
