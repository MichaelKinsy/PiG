### Fixed

- In the alt-screen, a wheel scroll reported in the legacy X10 mouse form (terminals without SGR mouse mode) no longer shows up as typed text when the pointer is at column or row 95 or higher; the report is counted in UTF-16 units as Pi counts it, so it scrolls. <!-- fix: 06cb94f85e -->
