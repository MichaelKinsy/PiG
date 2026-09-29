// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// Summary conversion uses the shared harness converter without provider normalization.
func convertToLlm(messages []agent.AgentMessage) []ai.Message {
	return harness.ConvertToLlm(messages)
}
