### Fixed

- Resuming or replacing a session whose saved model no catalog describes no longer continues on a blank model with no context window and no prices (a footer of `0.0%/0`). As in Pi, only a model the model runtime carries is restored; otherwise the session keeps the model it had. <!-- fix: 6691fb8df -->
