// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build !unix

package main

// haveRusage reports whether processCPU and peakResidentMB measure this process. Without getrusage the seed and sample
// commands refuse to run; the traced and timed runs record no process CPU time.
const haveRusage = false

func processCPU() float64 { return 0 }

func peakResidentMB() float64 { return 0 }
