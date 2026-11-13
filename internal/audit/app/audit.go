// Package app owns the typed audit-chain application contract and append rules.
package app

import (
	"context"

	"contractaudit/internal/audit"
)

type RunRef struct {
	ID string
}

type Event = audit.Event

type Draft struct {
	EventID        string
	RunID          string
	Actor          string
	EventType      string
	CorrelationIDs []string
}

type Stream uint8

const (
	AuditStream Stream = iota + 1
	BusinessAuditStream
)

type ReadFilter struct {
	CaseID *string
}

// Reader reads only Audit-owned event streams and review-run case references.
// Adapters may accept a read-only query capability; it need not support writes.
type Reader interface {
	ReadEvents(context.Context, ReadFilter) ([]Event, error)
	ReadHead(context.Context, RunRef) (Event, bool, error)
	ReadCaseHeads(context.Context, []string) ([]Event, error)
}

// Writer appends one event to the owner-selected stream in the caller's
// existing transaction.
type Writer interface {
	Append(context.Context, Stream, Event) error
}

type Service struct {
	Reader Reader
	Writer Writer
}

// SelectHead preserves the Store's historical selection rule: greatest
// sequence wins, and the last row wins if the in-memory slice has a tie.
func SelectHead(events []Event, ref RunRef) (Event, bool) {
	var head Event
	found := false
	for _, event := range events {
		if event.RunID != ref.ID || (found && event.Seq < head.Seq) {
			continue
		}
		head, found = event, true
	}
	return head, found
}

// PrepareAppend applies the canonical audit chain rule to a typed draft.
// Event IDs remain supplied by the caller so existing ID generation stays
// compatible with its established identity format.
func PrepareAppend(head Event, found bool, draft Draft) (Event, error) {
	if !found {
		head = Event{}
	}
	next := Event{
		EventID: draft.EventID, RunID: draft.RunID, Actor: draft.Actor,
		Seq: head.Seq + 1, EventType: draft.EventType,
		CorrelationIDs: append([]string(nil), draft.CorrelationIDs...),
	}
	return audit.Append(head, next)
}

func (s Service) Read(ctx context.Context, filter ReadFilter) ([]Event, error) {
	return s.Reader.ReadEvents(ctx, filter)
}

func (s Service) Head(ctx context.Context, ref RunRef) (Event, bool, error) {
	return s.Reader.ReadHead(ctx, ref)
}

func (s Service) CaseHeads(ctx context.Context, caseIDs []string) ([]Event, error) {
	return s.Reader.ReadCaseHeads(ctx, caseIDs)
}

func (s Service) Persist(ctx context.Context, stream Stream, events []Event) error {
	for _, event := range events {
		if err := s.Writer.Append(ctx, stream, event); err != nil {
			return err
		}
	}
	return nil
}
