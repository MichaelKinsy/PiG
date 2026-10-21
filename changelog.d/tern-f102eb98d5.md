### Fixed

- Fixed a chord link closed with a reason reporting "peer closed" to its close handlers instead when the peer answered the goodbye before the closing side had marked the link closed; the link now ends first, with the reason it was closed with, as upstream's does. <!-- fix: f102eb98d5 -->
