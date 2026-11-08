### Changed

- Loading a session decodes its messages faster, with the same result: a message was checked for well-formed JSON once for its record and again for its content and for each content block it holds, and now once. A 20 KB tool result and a 6 KB answer decode in 464 µs instead of 678 µs (-32%); a resumed session of 4,000 entries starts that much sooner.
