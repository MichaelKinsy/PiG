### Fixed

- On Windows, a session replacement whose working directory differs from the previous one only in letter case (`c:\work\proj` after `C:\Work\Proj`) keeps the loaded Node extension factories instead of dropping them and re-running every module, as Pi does (#11588). <!-- fix: 5af3a1803d -->
