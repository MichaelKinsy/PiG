#!/bin/sh
# Builds the benchmark image from the current commit and prints the digest-pinned reference the runner takes as
# -image or K8SBENCH_IMAGE, or, with BUNDLE, exports the artifact bundle the runner takes as -bundle or K8SBENCH_BUNDLE
# (no image is pushed and no registry is needed).
#
#   IMAGE=<registry>/<repository> bench/k8s/image/build.sh            build for linux/amd64 and push
#   IMAGE=... PLATFORM=linux/arm64 bench/k8s/image/build.sh             another node architecture
#   IMAGE=... PUSH=0 bench/k8s/image/build.sh                           build into the local image store only
#   IMAGE=... PI_REFERENCE=0 bench/k8s/image/build.sh                   leave out the Pi reference checkout
#   BUNDLE=<dir> bench/k8s/image/build.sh                               export the bundle to <dir> instead (Docker only)
#   K8SBENCH_ALLOW_DIRTY=1                                              build a tree with uncommitted or untracked files (commit gets -dirty)
set -eu
PLATFORM=${PLATFORM:-linux/amd64}
PUSH=${PUSH:-1}
root=$(git rev-parse --show-toplevel)
cd "$root"
commit=$(git rev-parse HEAD)
if [ -n "$(git status --porcelain)" ]; then
	if [ "${K8SBENCH_ALLOW_DIRTY:-}" != 1 ]; then
		echo "build.sh: the tree has uncommitted or untracked files; commit them or set K8SBENCH_ALLOW_DIRTY=1" >&2
		exit 1
	fi
	commit="$commit-dirty"
fi
if [ -n "${BUNDLE:-}" ]; then
	if [ -e "$BUNDLE" ] && [ -n "$(ls -A "$BUNDLE")" ]; then
		echo "build.sh: $BUNDLE is not empty" >&2
		exit 1
	fi
	docker buildx build --platform "$PLATFORM" -f bench/k8s/image/Dockerfile --target bundle \
		--build-arg PIG_COMMIT="$commit" --build-arg PI_REFERENCE=0 \
		--output "type=local,dest=$BUNDLE" .
	echo "$BUNDLE"
	exit 0
fi
: "${IMAGE:?set IMAGE to the registry repository to push to, or BUNDLE to a directory}"
tag="$IMAGE:$(echo "$commit" | cut -c1-12)$(case $commit in *-dirty) echo -dirty ;; esac)"
meta=$(mktemp)
trap 'rm -f "$meta"' EXIT
output="--load"
[ "$PUSH" = 1 ] && output="--push"
docker buildx build --platform "$PLATFORM" -f bench/k8s/image/Dockerfile \
	--build-arg PIG_COMMIT="$commit" --build-arg PI_REFERENCE="${PI_REFERENCE:-1}" \
	--metadata-file "$meta" -t "$tag" $output .
digest=$(sed -n 's/.*"containerimage.digest": *"\(sha256:[0-9a-f]*\)".*/\1/p' "$meta" | head -1)
[ -n "$digest" ] || { echo "build.sh: buildx reported no digest" >&2; exit 1; }
echo "$IMAGE@$digest"
