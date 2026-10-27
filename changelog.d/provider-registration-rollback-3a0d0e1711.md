### Fixed

- A `Provider` object that an extension registers while it loads is now applied after every extension has loaded, like a provider configuration, instead of at once. When the model registry rejects a registration, PiG now leaves nothing of it behind: the login it declared, its entry in the model list that extensions read, and its claim on the provider are given back, so a registration of the same provider by another extension stays as it was. Pi's registry validates a registration before it stores anything of it. <!-- fix: 3a0d0e17113b94d441248a87aa867ca135e7fcf1 -->
