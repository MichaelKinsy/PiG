### Fixed

- Durable tool calls that the model issues in parallel or nested no longer slow down as the live state grows: a commit no longer decodes the whole live document, and each tool reads only its own call.
