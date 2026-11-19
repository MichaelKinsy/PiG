### Fixed

- A Chord WebSocket link made over a socket that closed while the link registered its listeners is a closed link too; it used to stay open over the closed socket, because the close came after the link read the socket's state and before its `close` listener existed. <!-- fix: 647a44808aeb4c1e62c7009848a6d1ba6030c6e1 -->
