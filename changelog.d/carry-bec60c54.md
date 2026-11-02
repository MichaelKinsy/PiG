### Fixed

- `pig -e git:<repo>@<branch>` no longer stays on its first checkout. A temporary Git source with a ref is now fetched and reset to the ref on each start and `/reload`, as an unpinned source is; a commit-hash ref that is already checked out is not fetched. Offline mode and a failed refresh keep the cached checkout. Carried forward from Pi bec60c54 (earendil-works/pi#11375), past the 1.4.0+b3a29f11 pin. <!-- fix: bec60c54 -->
