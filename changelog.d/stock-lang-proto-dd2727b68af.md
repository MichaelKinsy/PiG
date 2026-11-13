### Fixed

- The write card and the expanded read card of a file whose extension is `constructor` or `__proto__` (for example `notes.constructor`) now draw what Pi draws: the tool name with its arguments for the write call, and the file's lines directly under the header for the read result. Pi looks the extension up in a plain JavaScript object, finds a member inherited from `Object.prototype`, its highlighter throws, and Pi falls back to its generic card; PiG looked the extension up in a Go map, found no language and drew the ordinary cards. <!-- fix: dd2727b68af -->
