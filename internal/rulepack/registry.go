package rulepack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

type Registry struct {
	mu      sync.RWMutex
	release map[registryKey]map[string]*Pack
	latest  map[registryKey]*Pack
}

type registryKey struct{ tenantID, packID string }

type registrySnapshot struct {
	SchemaVersion string            `json:"schema_version"`
	Packs         []json.RawMessage `json:"packs"`
}

// NewRegistry creates an in-memory immutable-release catalog.
func NewRegistry() *Registry {
	return &Registry{release: make(map[registryKey]map[string]*Pack), latest: make(map[registryKey]*Pack)}
}

// Publish adds an already published Pack and rejects version rollback or reuse.
func (r *Registry) Publish(pack *Pack) error {
	if r == nil {
		return errors.New("nil rulepack registry")
	}
	if pack == nil || pack.lifecycle.State != StatePublished {
		return ErrNotPublished
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.insert(pack)
}

// Lookup returns the immutable pack at an exact tenant, ID and version.
func (r *Registry) Lookup(tenantID, packID, version string) (*Pack, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := r.release[registryKey{tenantID: tenantID, packID: packID}]
	pack, ok := versions[version]
	return pack, ok
}

// Latest returns the greatest published numeric version for a tenant and pack ID.
func (r *Registry) Latest(tenantID, packID string) (*Pack, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	pack, ok := r.latest[registryKey{tenantID: tenantID, packID: packID}]
	return pack, ok
}

// Snapshot exports every published version in deterministic tenant/ID/version order.
func (r *Registry) Snapshot() ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil rulepack registry")
	}
	r.mu.RLock()
	packs := make([]*Pack, 0)
	for _, versions := range r.release {
		for _, pack := range versions {
			packs = append(packs, pack)
		}
	}
	r.mu.RUnlock()
	sort.Slice(packs, func(i, j int) bool {
		a, b := packs[i], packs[j]
		if a.content.TenantID != b.content.TenantID {
			return a.content.TenantID < b.content.TenantID
		}
		if a.content.ID != b.content.ID {
			return a.content.ID < b.content.ID
		}
		aVersion, _ := strconv.ParseUint(a.content.Version, 10, 64)
		bVersion, _ := strconv.ParseUint(b.content.Version, 10, 64)
		return aVersion < bVersion
	})
	snapshot := registrySnapshot{SchemaVersion: RegistrySchema, Packs: make([]json.RawMessage, 0, len(packs))}
	for _, pack := range packs {
		payload, err := pack.Snapshot()
		if err != nil {
			return nil, err
		}
		snapshot.Packs = append(snapshot.Packs, payload)
	}
	return json.Marshal(snapshot)
}

// Reload atomically imports a published snapshot without dropping or changing existing releases.
func (r *Registry) Reload(raw []byte) error {
	if r == nil {
		return errors.New("nil rulepack registry")
	}
	var snapshot registrySnapshot
	if err := decodeStrict(raw, &snapshot); err != nil {
		return fmt.Errorf("%w: registry snapshot: %v", ErrInvalidManifest, err)
	}
	if snapshot.SchemaVersion != RegistrySchema {
		return fmt.Errorf("%w: unsupported registry schema %q", ErrInvalidManifest, snapshot.SchemaVersion)
	}
	packs := make([]*Pack, 0, len(snapshot.Packs))
	for _, rawPack := range snapshot.Packs {
		pack, err := Parse(rawPack)
		if err != nil {
			return err
		}
		if pack.lifecycle.State != StatePublished {
			return ErrNotPublished
		}
		packs = append(packs, pack)
	}
	sort.Slice(packs, func(i, j int) bool {
		a, b := packs[i], packs[j]
		if a.content.TenantID != b.content.TenantID {
			return a.content.TenantID < b.content.TenantID
		}
		if a.content.ID != b.content.ID {
			return a.content.ID < b.content.ID
		}
		aVersion, _ := strconv.ParseUint(a.content.Version, 10, 64)
		bVersion, _ := strconv.ParseUint(b.content.Version, 10, 64)
		return aVersion < bVersion
	})
	replacement := NewRegistry()
	for _, pack := range packs {
		if err := replacement.insert(pack); err != nil {
			return err
		}
	}
	r.mu.Lock()
	if err := retainExistingReleases(r, replacement); err != nil {
		r.mu.Unlock()
		return err
	}
	r.release, r.latest = replacement.release, replacement.latest
	r.mu.Unlock()
	return nil
}

func retainExistingReleases(current, incoming *Registry) error {
	for key, versions := range current.release {
		incomingVersions := incoming.release[key]
		if len(incomingVersions) == 0 {
			return ErrVersionRollback
		}
		for version, existing := range versions {
			candidate, ok := incomingVersions[version]
			if !ok {
				return ErrVersionRollback
			}
			if existing.hash != candidate.hash {
				return ErrVersionReuse
			}
			existingSnapshot, _ := existing.Snapshot()
			candidateSnapshot, _ := candidate.Snapshot()
			if !bytes.Equal(existingSnapshot, candidateSnapshot) {
				return ErrVersionReuse
			}
		}
		currentLatest, ok := current.latest[key]
		if !ok {
			return ErrVersionRollback
		}
		incomingLatest, ok := incoming.latest[key]
		if !ok {
			return ErrVersionRollback
		}
		currentVersion, _ := strconv.ParseUint(currentLatest.content.Version, 10, 64)
		incomingVersion, _ := strconv.ParseUint(incomingLatest.content.Version, 10, 64)
		if incomingVersion < currentVersion {
			return ErrVersionRollback
		}
		for version := range incomingVersions {
			if _, retained := versions[version]; retained {
				continue
			}
			parsedVersion, _ := strconv.ParseUint(version, 10, 64)
			if parsedVersion <= currentVersion {
				return ErrVersionRollback
			}
		}
	}
	return nil
}

func (r *Registry) insert(pack *Pack) error {
	key := registryKey{tenantID: pack.content.TenantID, packID: pack.content.ID}
	version, err := strconv.ParseUint(pack.content.Version, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: invalid pack version", ErrInvalidManifest)
	}
	if r.release == nil {
		r.release = make(map[registryKey]map[string]*Pack)
	}
	if r.latest == nil {
		r.latest = make(map[registryKey]*Pack)
	}

	versions := r.release[key]
	if versions == nil {
		versions = make(map[string]*Pack)
		r.release[key] = versions
	}
	if existing, exists := versions[pack.content.Version]; exists {
		if existing.hash != pack.hash {
			return ErrVersionReuse
		}
		existingSnapshot, _ := existing.Snapshot()
		candidateSnapshot, _ := pack.Snapshot()
		if !bytes.Equal(existingSnapshot, candidateSnapshot) {
			return ErrVersionReuse
		}
		return nil
	}
	if latest := r.latest[key]; latest != nil {
		latestVersion, _ := strconv.ParseUint(latest.content.Version, 10, 64)
		if version < latestVersion {
			return ErrVersionRollback
		}
	}
	versions[pack.content.Version] = pack
	if latest := r.latest[key]; latest == nil {
		r.latest[key] = pack
	} else {
		latestVersion, _ := strconv.ParseUint(latest.content.Version, 10, 64)
		if version > latestVersion {
			r.latest[key] = pack
		}
	}
	return nil
}
