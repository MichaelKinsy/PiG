### Changed

- A finished tool call whose arguments are a valid JSON object is parsed once instead of three times (strict check, repair, strict parse). A 200 KB `write` call gets from the end of its stream to its result about 3.5 ms sooner. Arguments that are not a valid object still go through Pi's repair and partial parse, with the same result and error text.
