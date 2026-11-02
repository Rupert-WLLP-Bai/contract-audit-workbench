// File metadata (manifest mirror):
// Purpose: Reads EXECUTION_QUEUE_POLICY, returns store.DefaultQueuePolicy when unset, and otherwise accepts exactly one strict JSON object up to 32 KiB; malformed, oversized, unknown-field or trailing-value input is rejected.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-10-25
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package bootstrap

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"contractaudit/internal/store"
)

func configuredQueuePolicy() (store.QueuePolicy, error) {
	raw := strings.TrimSpace(os.Getenv("EXECUTION_QUEUE_POLICY"))
	if raw == "" {
		return store.DefaultQueuePolicy(), nil
	}
	if len(raw) > 32768 {
		return store.QueuePolicy{}, errors.New("execution queue policy is too large")
	}
	var policy store.QueuePolicy
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, errors.New("invalid execution queue policy JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return policy, errors.New("execution queue policy must be one JSON object")
	}
	return policy, nil
}
