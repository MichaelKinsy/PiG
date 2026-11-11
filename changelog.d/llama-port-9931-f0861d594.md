### Changed

- The default llama.cpp server URL is now `http://127.0.0.1:9931`, the port the current llama.cpp server listens on, as in Pi (#12001): `/llama`, the `llama.cpp` provider and the setup text use it when neither a saved credential nor `LLAMA_BASE_URL` names a server. If you run llama.cpp on port 8080, set `LLAMA_BASE_URL=http://127.0.0.1:8080` (or save that URL in the `llama.cpp` credential). The llama.cpp and NVIDIA RTX guides use 9931 and no longer pass `--port 8080`.
