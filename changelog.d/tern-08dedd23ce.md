### Fixed

- A model reply with many tool calls no longer slows down as calls finish: finished calls are not parsed again on every later stream event.
