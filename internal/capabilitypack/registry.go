package capabilitypack

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"

	"contractaudit/internal/auditext"
)

// RegistryConfig lists the host-registered references a manifest may use. The
// lists are copied; later caller mutation does not change the registry.
type RegistryConfig struct {
	AllowedCapabilities []string
	AllowedConnectors   []string
	AllowedComparators  []string
}

// Registry is an in-memory catalog of validated capability pack manifests
// keyed by pack_id and version. A registry only accepts references declared in
// its RegistryConfig; unknown capabilities, connectors and comparators are
// rejected rather than resolved at execution time.
//
// A Registry is safe for concurrent use.
type Registry struct {
	mu sync.RWMutex

	allowedCapabilities map[string]struct{}
	allowedConnectors   map[string]struct{}
	allowedComparators  map[string]struct{}

	packs map[string]map[string]Manifest
}

// NewRegistry builds an empty registry from the host allow-lists.
func NewRegistry(config RegistryConfig) (*Registry, error) {
	capabilities, err := referenceSet("capability", config.AllowedCapabilities)
	if err != nil {
		return nil, err
	}
	connectors, err := referenceSet("connector", config.AllowedConnectors)
	if err != nil {
		return nil, err
	}
	comparators, err := referenceSet("comparator", config.AllowedComparators)
	if err != nil {
		return nil, err
	}
	return &Registry{
		allowedCapabilities: capabilities,
		allowedConnectors:   connectors,
		allowedComparators:  comparators,
		packs:               make(map[string]map[string]Manifest),
	}, nil
}

func referenceSet(kind string, values []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("%w: empty allowed %s reference", auditext.ErrInvalidRequest, kind)
		}
		set[value] = struct{}{}
	}
	return set, nil
}

// Register validates a manifest and adds it to the catalog. A manifest that
// references an unregistered capability, connector or comparator is rejected,
// as is a second registration of the same pack_id and version.
func (r *Registry) Register(m Manifest) error {
	if r == nil {
		return fmt.Errorf("%w: nil capability pack registry", auditext.ErrInvalidRequest)
	}
	if err := m.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkReferences(m); err != nil {
		return err
	}
	if r.packs == nil {
		r.packs = make(map[string]map[string]Manifest)
	}
	versions := r.packs[m.PackID]
	if versions == nil {
		versions = make(map[string]Manifest)
		r.packs[m.PackID] = versions
	}
	if _, exists := versions[m.Version]; exists {
		return fmt.Errorf("%w: capability pack %s@%s is already registered", auditext.ErrInvalidRequest, m.PackID, m.Version)
	}
	versions[m.Version] = cloneManifest(m)
	return nil
}

func (r *Registry) checkReferences(m Manifest) error {
	for _, capability := range m.Capabilities {
		if _, ok := r.allowedCapabilities[capability.ID]; !ok {
			return fmt.Errorf("%w: capability %q is not registered", auditext.ErrInvalidRequest, capability.ID)
		}
		if capability.ConnectorRef != nil && *capability.ConnectorRef != "" {
			if _, ok := r.allowedConnectors[*capability.ConnectorRef]; !ok {
				return fmt.Errorf("%w: connector %q is not registered", auditext.ErrInvalidRequest, *capability.ConnectorRef)
			}
		}
	}
	for _, check := range m.Checks {
		if _, ok := r.allowedComparators[check.ComparatorRef]; !ok {
			return fmt.Errorf("%w: comparator %q is not registered", auditext.ErrInvalidRequest, check.ComparatorRef)
		}
	}
	return nil
}

// Get returns a copy of the registered manifest for an exact pack_id and
// version.
func (r *Registry) Get(packID, version string) (Manifest, bool) {
	if r == nil {
		return Manifest{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	versions, ok := r.packs[packID]
	if !ok {
		return Manifest{}, false
	}
	m, ok := versions[version]
	if !ok {
		return Manifest{}, false
	}
	return cloneManifest(m), true
}

// List returns copies of all registered manifests sorted by pack_id and then
// version, so callers can paginate or render a stable catalog.
func (r *Registry) List() []Manifest {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	manifests := make([]Manifest, 0)
	for _, versions := range r.packs {
		for _, m := range versions {
			manifests = append(manifests, cloneManifest(m))
		}
	}
	slices.SortFunc(manifests, func(a, b Manifest) int {
		if order := strings.Compare(a.PackID, b.PackID); order != 0 {
			return order
		}
		return compareVersions(a.Version, b.Version)
	})
	return manifests
}

// compareVersions compares validated three-part numeric versions without a
// machine-integer limit. Raw spelling breaks numeric ties so List stays stable
// when distinct registered versions differ only in leading zeroes.
func compareVersions(a, b string) int {
	segmentsB := strings.Split(b, ".")
	for i, segment := range strings.Split(a, ".") {
		left := strings.TrimLeft(segment, "0")
		right := strings.TrimLeft(segmentsB[i], "0")
		if order := cmp.Compare(len(left), len(right)); order != 0 {
			return order
		}
		if order := strings.Compare(left, right); order != 0 {
			return order
		}
	}
	return strings.Compare(a, b)
}

func cloneManifest(m Manifest) Manifest {
	clone := m
	clone.RequiredMaterialRoles = append([]string(nil), m.RequiredMaterialRoles...)
	if m.Capabilities != nil {
		clone.Capabilities = make([]Capability, len(m.Capabilities))
		for i, capability := range m.Capabilities {
			clone.Capabilities[i] = capability
			if capability.ConnectorRef != nil {
				connector := *capability.ConnectorRef
				clone.Capabilities[i].ConnectorRef = &connector
			}
		}
	}
	if m.Checks != nil {
		clone.Checks = make([]Check, len(m.Checks))
		for i, check := range m.Checks {
			clone.Checks[i] = check
			if check.ExternalField != nil {
				field := *check.ExternalField
				clone.Checks[i].ExternalField = &field
			}
		}
	}
	clone.UI.Fields = append([]string(nil), m.UI.Fields...)
	return clone
}
