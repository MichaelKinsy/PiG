#!/bin/sh
# Stands in for a slow build: prints progress, then writes one artifact.
set -e
mkdir -p dist
echo "compiling..."
sleep 20
printf 'pig-build-artifact-v1\n' > dist/app.bin
echo "build finished: dist/app.bin"
