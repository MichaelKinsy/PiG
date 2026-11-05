### Fixed

- An unreachable llama.cpp server now reports `Could not reach llama.cpp server at <url>: <cause>` (in `/login llama.cpp`, `/llama` and the model refresh) instead of `fetch failed`, as in Pi. <!-- fix: fa4a558a1b -->
