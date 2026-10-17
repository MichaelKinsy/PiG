// SPDX-License-Identifier: MIT

//go:build race

package main

// raceEnabled is true in a -race build, where seeding the 299 MiB fixtures takes minutes.
const raceEnabled = true
