### Fixed

- A Mistral request that names a function as its tool choice writes the object `type, function`, as Pi does; PiG wrote `function, type`. The content was the same. Only an extension or SDK caller that passes a tool choice object reaches this. <!-- fix: 4de6fbaf00 -->
