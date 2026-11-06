### Fixed

- `pig piglet build` of an ahead-of-time image no longer waits 200 ms per extension after each training run's registration, and no longer closes the training connection in the middle of a registration frame that arrives in more than one piece. <!-- fix: 471d6fb3bd -->
