### Fixed

- An extension that calls the base autocomplete provider's `shouldTriggerFileCompletion` with a negative cursor column now gets Pi's answer: the column counts back from the end of the line in UTF-16 units as `line.slice(0, col)` does, where it was treated as column 0. <!-- fix: 7689b82bf75082d659d4865272ee4fbe53876724 -->
