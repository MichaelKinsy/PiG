### Fixed

- `pig build` now raises the merged `go.mod` `go` directive to the highest `go` version required by any fused member module (compared with `go/version`), preventing `-mod=readonly` failures when a fused member requires a newer Go language or patch version than PiG's own directive (PR #446). <!-- fix: 7184929b8e --> <!-- cause: coding/pigletbuild/fuse_modules.go merged member require/replace directives without raising pig.Go when a fused member required a newer go directive -->
