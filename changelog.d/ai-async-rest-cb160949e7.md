### Fixed

- ai: `Models.GenerateImages` given a model of another api than its images provider's ends with the error `Mismatched api: <model api> expected <api>`, as Pi's `generateImages` returns the images registry's refusal as an error result; PiG 0.4.0 to 0.7.0 ran the provider with that model. <!-- fix: cb160949e7 -->
