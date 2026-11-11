# Builds and runs the PiG desktop app (apps/p2desktop) in the foreground (Ctrl-C quits it and stops its server). See README.md.
# Pi's p2-mac Makefile (Pi main 3a647d9dc) ported to this app: the server runs in the app's own process, so there is no
# separate server to attach to and no checkout to choose.
#
#   make run          own server, local only: data in ~/.pi/p2-desktop/local (FAKE=1: faux provider)
#   make run-radius   own server connected to Radius: data in ~/.pi/p2-desktop/radius (signs in on first use)
#
# p2 runs one server per data directory and Radius is on exactly when <data>/radius.json exists, so each target gets its own
# data directory (and its own state directory, which keeps the port of its last launch) and leaves `p2 server` on ~/.pi/p2 alone.

P2 ?= $(abspath ../p2)

LOCAL_DATA_DIR ?= $(HOME)/.pi/p2-desktop/local
RADIUS_DATA_DIR ?= $(HOME)/.pi/p2-desktop/radius
FAKE ?=

.PHONY: build run run-radius radius-login clean

build:
	go build -o build/p2-desktop ./cmd/p2-desktop

run: build
	mkdir -p -m 700 "$(LOCAL_DATA_DIR)"
	P2_DATA_DIR="$(LOCAL_DATA_DIR)" P2_DESKTOP_STATE_DIR="$(LOCAL_DATA_DIR)/desktop" $(if $(FAKE),P2_FAKE=1) build/p2-desktop

run-radius: build
	@test -f "$(RADIUS_DATA_DIR)/radius.json" || $(MAKE) radius-login
	P2_DATA_DIR="$(RADIUS_DATA_DIR)" P2_DESKTOP_STATE_DIR="$(RADIUS_DATA_DIR)/desktop" build/p2-desktop

# Device-code sign-in to Radius for the radius data directory (PI_RADIUS_GATEWAY picks another gateway).
radius-login:
	mkdir -p -m 700 "$(RADIUS_DATA_DIR)"
	cd "$(P2)" && P2_DATA_DIR="$(RADIUS_DATA_DIR)" go run ./cmd/p2 radius-login

clean:
	rm -rf build
