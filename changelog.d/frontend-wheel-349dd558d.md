### Added

- A frontend member can now turn the mouse wheel over an extension's overlay or dock view with `Env.Wheel(frontend.Wheel{Node, Row, Column, ViewPath, Delta})`, the wheel counterpart of `Env.Click`. PiG hands the extension's component one wheel event at that cell with `Delta` as its `wheelDelta` (negative up), as Pi's fullscreen renderer offers a terminal wheel report to the component under the pointer first. It does nothing for a node that is not an extension view, a cell outside it, a `Delta` of 0, or outside fullscreen mode.
