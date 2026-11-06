### Changed

- The `pig` binary is about 1.39 MB smaller: the word-segmentation dictionaries (Chinese, Japanese, Thai, Lao, Khmer and Burmese) store one 3-byte offset for each block of 16 words, not a 4-byte offset for each word. Word movement and deletion in the editor, double-click word selection and tool search segment text as fast as before, and a process holds less file-backed memory after its first segmentation.
