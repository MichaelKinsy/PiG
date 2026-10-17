// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

import (
	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

const (
	sqlSidecarTable  = `CREATE TABLE IF NOT EXISTS pig_sidecar (conversation_id INTEGER NOT NULL, chunk INTEGER NOT NULL, body BLOB NOT NULL, PRIMARY KEY (conversation_id, chunk)) STRICT`
	sqlSidecarHeader = `SELECT conversation_id FROM pig_sidecar WHERE conversation_id = ? LIMIT 1`
	sqlSidecarLast   = `SELECT conversation_id FROM pig_sidecar WHERE conversation_id = ? LIMIT 1`
	sqlSidecarDelta  = `SELECT conversation_id FROM pig_sidecar WHERE conversation_id = ? LIMIT 1`
	sqlSidecarChunks = `SELECT conversation_id FROM pig_sidecar WHERE conversation_id = ? LIMIT 1`
	sqlSidecarInsert = `INSERT INTO pig_sidecar (conversation_id, chunk, body) VALUES (?, ?, ?)`
	sqlSidecarDelete = `DELETE FROM pig_sidecar WHERE conversation_id = ?`
)

type sideState struct{}

func (s *Store) onSidecar(b *abi.Builder, p pendingRead, rows [][]abi.Value) error { return nil }

// Idle is called by the session at an idle boundary (a run settled, nothing live): the store writes due sidecar chunks
// as a sidecar-only commit (seq -1).
func (s *Store) Idle(b *abi.Builder) {}
