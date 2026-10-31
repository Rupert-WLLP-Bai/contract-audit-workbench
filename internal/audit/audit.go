// Package audit is the append-only review record.
// A snapshot is the immutable input of one run. An event hash covers the
// previous hash, so a changed actor or a skipped sequence fails verification.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

var (
	// ErrBrokenChain means the sequence skipped or the digest does not match.
	ErrBrokenChain = errors.New("audit chain is broken")
	// ErrImmutable means a stored snapshot would be replaced.
	ErrImmutable = errors.New("review run snapshot is immutable")
)

// Event is one appended audit record. Hash is sha256 of the canonical lines
// event_id, run_id, actor, seq, event_type, prev_hash, and correlation ids
// joined by commas.
type Event struct {
	EventID        string
	RunID          string
	Actor          string
	Seq            int
	EventType      string
	PrevHash       string
	Hash           string
	CorrelationIDs []string
}

// Snapshot is the immutable input set of one review run.
type Snapshot struct {
	DocumentIDs              []string
	ParsedDocumentIDs        []string
	PartyContextHash         string
	ServiceObjectContextHash string
	ParseSelectionHash       string
	ParserConfigurationID    string
	RuleSetHash              string
	PlaybookHash             string
	ModelRef                 string
	ModelConfigurationID     string
	ComparatorRef            string
	RulePackCompilerRef      string
	RulePackID               string
	RulePackVersion          string
	RulePackHash             string
	RulePackDigest           string
	PlannerConfigurationID   string
}

// Append returns next chained after prev. The first event uses seq 1 and an
// empty previous hash. next.Hash is ignored and recomputed.
func Append(prev, next Event) (Event, error) {
	if !completeEvent(next) || !continuesChain(prev, next) {
		return Event{}, ErrBrokenChain
	}
	next.PrevHash = prev.Hash
	next.Hash = digest(next)
	return next, nil
}

func completeEvent(event Event) bool {
	return event.EventID != "" && event.RunID != "" && event.Actor != "" && event.EventType != "" && event.Seq > 0
}

func continuesChain(prev, next Event) bool {
	if prev.Seq == 0 {
		return next.Seq == 1
	}
	return next.Seq == prev.Seq+1 && next.RunID == prev.RunID
}

// Verify recomputes the chain. The first event must be sequence 1.
func Verify(events []Event) error {
	var prev Event
	for _, event := range events {
		want, err := Append(prev, Event{
			EventID: event.EventID, RunID: event.RunID, Actor: event.Actor, Seq: event.Seq,
			EventType: event.EventType, CorrelationIDs: event.CorrelationIDs,
		})
		if err != nil {
			return err
		}
		if want.Hash != event.Hash || want.PrevHash != event.PrevHash {
			return ErrBrokenChain
		}
		prev = event
	}
	return nil
}

// Freeze copies the snapshot and sorts document ids. The hashes and the
// comparator are required; the model ref may be empty when no model ran.
func Freeze(in Snapshot) (Snapshot, error) {
	if len(in.DocumentIDs) == 0 || in.RuleSetHash == "" || in.PlaybookHash == "" || in.ComparatorRef == "" {
		return Snapshot{}, ErrImmutable
	}
	ids := append([]string(nil), in.DocumentIDs...)
	sortStrings(ids)
	in.DocumentIDs = ids
	parsedIDs := append([]string(nil), in.ParsedDocumentIDs...)
	sortStrings(parsedIDs)
	in.ParsedDocumentIDs = parsedIDs
	return in, nil
}

// RejectRewrite returns ErrImmutable when next is not the stored snapshot.
func RejectRewrite(stored, next Snapshot) error {
	if !same(stored, next) {
		return ErrImmutable
	}
	return nil
}

func digest(event Event) string {
	payload := strings.Join([]string{
		event.EventID,
		event.RunID,
		event.Actor,
		strconv.Itoa(event.Seq),
		event.EventType,
		event.PrevHash,
		strings.Join(event.CorrelationIDs, ","),
	}, "\n")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func same(a, b Snapshot) bool {
	if !sameRuleHashes(a, b) || !sameModelAndComparator(a, b) ||
		!sameRulePack(a, b) || !sameParseAndPlannerConfiguration(a, b) ||
		a.PartyContextHash != b.PartyContextHash || a.ServiceObjectContextHash != b.ServiceObjectContextHash {
		return false
	}
	return equalStrings(a.DocumentIDs, b.DocumentIDs) && equalStrings(a.ParsedDocumentIDs, b.ParsedDocumentIDs)
}

func sameRuleHashes(a, b Snapshot) bool {
	return a.RuleSetHash == b.RuleSetHash && a.PlaybookHash == b.PlaybookHash
}

func sameModelAndComparator(a, b Snapshot) bool {
	return a.ModelRef == b.ModelRef && a.ModelConfigurationID == b.ModelConfigurationID && a.ComparatorRef == b.ComparatorRef
}

func sameRulePack(a, b Snapshot) bool {
	return a.RulePackCompilerRef == b.RulePackCompilerRef && a.RulePackID == b.RulePackID &&
		a.RulePackVersion == b.RulePackVersion && a.RulePackHash == b.RulePackHash && a.RulePackDigest == b.RulePackDigest
}

func sameParseAndPlannerConfiguration(a, b Snapshot) bool {
	return a.PlannerConfigurationID == b.PlannerConfigurationID &&
		a.ParseSelectionHash == b.ParseSelectionHash && a.ParserConfigurationID == b.ParserConfigurationID
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && items[j] < items[j-1] {
			items[j], items[j-1] = items[j-1], items[j]
			j--
		}
	}
}
