### Fixed

- Fullscreen Kitty images no longer disappear after a full redraw such as a window resize; the redraw deletes the placements and keeps the uploaded image data instead of clearing the screen, as Pi does (upstream #12047). <!-- fix: 97711dbbc6c5608a8254be3a7039bd47f943eb3c -->
