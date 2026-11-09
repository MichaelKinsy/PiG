### Fixed

- p2's terminal client no longer keeps showing an answer as it looked when it was last drawn while it was still arriving. On a loaded machine a frame drawn between the model's steps left the reply as an empty or "Thinking..." block until something else (a resize) redrew it, although the server and the browser had the finished answer. <!-- fix: eaa0202b4c5ad66915092f1839001f90268fe770 -->
