#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Builds libxcb 1.16.1 where the runner's libxcb is older (ubuntu-24.04 ships 1.15): 1.16 changed how DISPLAY is read, and the
# X11 display tests in internal/nativeplatform compare PiG with the libxcb pkg-config finds.
# Prints the PKG_CONFIG_PATH to use and appends it to $GITHUB_ENV when set. PIG_LIBXCB_PREFIX overrides the install directory.

version=1.16.1
sha256=f24d187154c8e027b358fc7cb6588e35e33e6a92f11c668fe77396a7ae66e311
url="https://xorg.freedesktop.org/archive/individual/lib/libxcb-${version}.tar.xz"
prefix="${PIG_LIBXCB_PREFIX:-${HOME}/.cache/pig-dev/libxcb-${version}}"

if [[ ! -f "${prefix}/lib/pkgconfig/xcb.pc" ]]; then
  work="$(mktemp -d)"
  trap 'rm -rf "${work}"' EXIT
  curl --fail --silent --show-error --location --retry 5 --retry-all-errors --output "${work}/libxcb.tar.xz" "${url}"
  echo "${sha256}  ${work}/libxcb.tar.xz" | sha256sum --check --status || { echo "build-libxcb: checksum mismatch for ${url}" >&2; exit 1; }
  tar -xJf "${work}/libxcb.tar.xz" -C "${work}"
  # libxcb generates its sources with xcb-proto's xcbgen module, so configure needs a python that imports it. The python3
  # first in PATH need not be one (actions/setup-python puts its own first, and apt installs xcbgen for /usr/bin/python3
  # only); xcb-proto.pc's pythondir does not help, as Ubuntu's names a directory apt does not install to.
  python=""
  for candidate in "$(command -v python3 || true)" /usr/bin/python3; do
    if [[ -n "${candidate}" ]] && "${candidate}" -c 'import xcbgen' 2>/dev/null; then
      python="${candidate}"
      break
    fi
  done
  [[ -n "${python}" ]] || { echo "build-libxcb: no python3 imports xcb-proto's xcbgen; install xcb-proto and python3-xcbgen" >&2; exit 1; }
  (
    cd "${work}/libxcb-${version}"
    ./configure PYTHON="${python}" --prefix="${prefix}" --disable-static --disable-devel-docs --without-doxygen >"${work}/configure.log" 2>&1 || { tail -30 "${work}/configure.log" >&2; exit 1; }
    make -j"$(nproc)" >"${work}/make.log" 2>&1 || { tail -30 "${work}/make.log" >&2; exit 1; }
    make install >>"${work}/make.log" 2>&1 || { tail -30 "${work}/make.log" >&2; exit 1; }
  )
fi

pkg_config_path="${prefix}/lib/pkgconfig${PKG_CONFIG_PATH:+:${PKG_CONFIG_PATH}}"
echo "PKG_CONFIG_PATH=${pkg_config_path}"
if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "PKG_CONFIG_PATH=${pkg_config_path}" >>"${GITHUB_ENV}"
fi
