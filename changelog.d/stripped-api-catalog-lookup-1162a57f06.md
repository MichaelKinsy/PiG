### Fixed

- A Piglet built with any model API stripped no longer allocates on every model catalog lookup. The built-in chat catalog asked the strip state for a fresh copy of the stripped API list each time it was read, to see whether the list had changed; it now compares one counter that the strip state advances when it changes, and reads the list only to rebuild. A strip applied after the first lookup (a Piglet's strip list is applied at start-up) is still seen by the next lookup. Stock PiG strips nothing and is unchanged. The bug was present since v0.7.0. <!-- fix: 1162a57f06 -->
