// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import "os"

// stopProfiles writes the PIG_PROFILE profiles that main started. It is a no-op
// when PIG_PROFILE is unset.
var stopProfiles = func() {}

// exitProcess releases startup extension ownership and writes profiles before
// exiting. os.Exit skips deferred calls, so main uses this on every exit path.
func exitProcess(code int) {
	stopStartupExtensions()
	stopProfiles()
	os.Exit(code)
}
