### Fixed

- Preserve Kitty printable-key deduplication across an empty stdin-buffer flush.
- Match Bedrock's ECMAScript blank-text rules for user, assistant, thinking, signature, and tool-result content. Sanitize assistant text before checking whether it is empty.
- Correct terminal progress-clear escape sequences.
- Preserve path prefixes, quotes, and directory cursor positions during completion. Keep direct `.git` completion available, disable attachment suggestions when `fd` is unavailable, and isolate concurrent completion sorting state.
- Match terminal image metadata, cursor and aspect-ratio defaults, and width-bounded fallback paths.
- Frame terminal input before negotiation and native-key normalization. Hold split keyboard replies for Pi's fragment deadline, preserve listener and read/paint ordering, and join pending readers and progress callbacks before returning terminal ownership.
- Share terminal background queries across automatic theme detection and explicit queries. Consume replies before editor work, retain late-reply ordering after timeouts, and match Pi's color-channel parsing.
- Show saved and current-session trust separately in `/trust`. Preserve the saved-choice marker while browsing, support inherited parent decisions, and surface trust-store failures without activating a new decision in the current session.
