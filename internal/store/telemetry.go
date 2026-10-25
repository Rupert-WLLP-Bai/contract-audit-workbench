package store

import (
	"contractaudit/internal/execution"
	"contractaudit/internal/telemetry"
)

// UseTelemetry transfers lifecycle ownership to Store. Configure before serving.
func (s *Store) UseTelemetry(exporter *telemetry.Exporter) {
	s.mu.Lock()
	previous := s.telemetry
	s.telemetry = exporter
	s.mu.Unlock()
	if previous != exporter {
		previous.Close()
	}
}

func (s *Store) recordTelemetry(runID, attemptID string, event execution.Event) {
	s.mu.Lock()
	exporter := s.telemetry
	s.mu.Unlock()
	exporter.Record(runID, attemptID, event)
}

func (s *Store) closeTelemetry() {
	s.UseTelemetry(nil)
}
