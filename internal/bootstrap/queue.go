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
