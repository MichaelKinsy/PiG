### Fixed

- The edit tool keeps the line endings of the lines it does not touch in files with mixed CRLF, LF or lone CR endings, and computes its diff and patch before writing, around the changes only, so a diff failure leaves the file unchanged. <!-- fix: 3b9c7daf85 -->
