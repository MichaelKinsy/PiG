### Changed

- An idle interactive session holds less memory: when it goes idle after a run, it lets go of the encodings of the conversation that the last requests reused (about 250 KB less live heap at the prompt of a six-prompt session). The next prompt encodes its request as the first one did. Requests are unchanged.
