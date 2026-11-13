### Fixed

- Chord: the rollout Step of a published script set now lists removed plugins in UTF-16 code unit order, as Pi does, instead of UTF-8 byte order; names outside the Basic Multilingual Plane no longer sort differently. <!-- fix: 2f8638b9498101639722008b729aca56d9187ac6 -->
- Chord: the HTTP listener now parses the Host header and the request target with the WHATWG URL algorithm, as Node does, instead of an approximation. <!-- fix: 2f8638b9498101639722008b729aca56d9187ac6 -->
- Chord: the HTTP listener answers 400 for a request target that contains bytes Node refuses, where it used to pass the request to the component. <!-- fix: 2f8638b9498101639722008b729aca56d9187ac6 -->
