### Fixed

- A Chord HTTP upgrade whose router check read a router that was composed but not yet delivered when the connection was kept could be ended by a delivery that was older than that router, which Pi never does. Routers now carry the order of their composition, and a connection is judged only by the routers composed after the one its check read, whether or not that one is ever delivered; a router composed later than the check still ends it. <!-- fix: 57347b2237078b3b0c0efe80bd92f3d62ca2f6c1 -->
