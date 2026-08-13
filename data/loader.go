package data

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrInvalidVersion reports an empty version name or nil factory.
	ErrInvalidVersion = errors.New("invalid data version")
	// ErrDuplicateVersion reports a version name that is already registered.
	ErrDuplicateVersion = errors.New("data version already registered")
	// ErrUnknownVersion reports a version name that is not registered.
	ErrUnknownVersion = errors.New("unknown data version")
	// ErrNilSet reports a factory that returned no error and a nil set.
	ErrNilSet = errors.New("data factory returned nil set")
)

// Factory constructs a fresh game-data set.
type Factory func() (*Set, error)

// Registry stores game-data factories by version name.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry creates an empty version registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a factory under a version name.
func (r *Registry) Register(name string, factory Factory) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidVersion)
	}
	if factory == nil {
		return fmt.Errorf("%w: nil factory for %q", ErrInvalidVersion, name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.factories == nil {
		r.factories = make(map[string]Factory)
	}
	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateVersion, name)
	}

	r.factories[name] = factory
	return nil
}

// Load constructs a fresh set for a registered version.
func (r *Registry) Load(name string) (*Set, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrUnknownVersion, name)
	}

	set, err := factory()
	if err != nil {
		return nil, fmt.Errorf("load data version %q: %w", name, err)
	}
	if set == nil {
		return nil, fmt.Errorf("%w: %q", ErrNilSet, name)
	}

	return set, nil
}

// Versions returns registered version names in sorted order. The caller owns
// the returned slice.
func (r *Registry) Versions() []string {
	r.mu.RLock()
	versions := make([]string, 0, len(r.factories))
	for version := range r.factories {
		versions = append(versions, version)
	}
	r.mu.RUnlock()

	sort.Strings(versions)
	return versions
}

var defaultRegistry = NewRegistry()

// Register adds a factory to the package-level version registry.
func Register(name string, factory Factory) error {
	return defaultRegistry.Register(name, factory)
}

// Load constructs a fresh set from the package-level version registry.
func Load(name string) (*Set, error) {
	return defaultRegistry.Load(name)
}

// RegisteredVersions returns package-level version names in sorted order. The
// caller owns the returned slice.
func RegisteredVersions() []string {
	return defaultRegistry.Versions()
}
