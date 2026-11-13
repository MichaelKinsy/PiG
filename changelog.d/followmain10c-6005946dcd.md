### Fixed

- chord: base bundles collect the fonts and images their stylesheets name: a base stylesheet with `url()` fonts, images or WebAssembly no longer fails to build with "No loader is configured"; they are files under `assets/`, named by their content (Pi da44c4df3). <!-- fix: 6005946dcd -->
