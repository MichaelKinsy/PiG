### Fixed

- A Kitty image in a padded or colored box, or with text beside it, is no longer dropped from the fullscreen view once its first row scrolls above the viewport: the visible rows are cropped. <!-- fix: 72e0ca2b6b -->
- Gemini tool schemas send string and number `const` values as single-value `enum`s, which Gemini honors where it ignores `const`; literal data such as `default` and `examples` is left as written. <!-- fix: 9d02a488f0 -->
