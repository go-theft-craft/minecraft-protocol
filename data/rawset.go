package data

import (
	"fmt"
	"slices"
	"sort"
)

// RawSet is every dataset a version was generated from, kept as the bytes
// upstream published, together with the version they describe.
//
// A typed registry is an interpretation: it keeps what this repository decided
// to model and drops the rest. Upstream ships datasets nothing here models yet
// — loot tables, sounds, tints, the command tree — and a consumer that needs
// one should be able to read it rather than wait for a typed accessor. Keeping
// the bytes is also what makes the interpretation checkable: the source of a
// generated value is still there to compare against.
type RawSet struct {
	version  Version
	datasets map[string]RawDataset
	names    []string
}

// NewRawSet returns a RawSet that does not retain caller-owned bytes.
func NewRawSet(version Version, datasets []RawDataset) (*RawSet, error) {
	stored := make(map[string]RawDataset, len(datasets))
	names := make([]string, 0, len(datasets))

	for _, dataset := range datasets {
		if dataset.Name == "" {
			return nil, fmt.Errorf("%w: empty name", ErrInvalidDataset)
		}
		if _, exists := stored[dataset.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate name %q", ErrInvalidDataset, dataset.Name)
		}
		stored[dataset.Name] = dataset.Clone()
		names = append(names, dataset.Name)
	}
	sort.Strings(names)

	return &RawSet{version: version, datasets: stored, names: names}, nil
}

// Version returns the version the datasets describe.
func (r *RawSet) Version() Version {
	if r == nil {
		return Version{}
	}

	return r.version
}

// Names returns every dataset name in sorted order. The caller owns the slice,
// and the order is stable so a caller can diff two versions' inventories.
func (r *RawSet) Names() []string {
	if r == nil {
		return nil
	}

	return slices.Clone(r.names)
}

// Len reports how many datasets the set holds.
func (r *RawSet) Len() int {
	if r == nil {
		return 0
	}

	return len(r.names)
}

// Get returns a copy of the named dataset. The copy matters: the bytes are the
// pinned upstream record, and a caller that could edit them in place would be
// editing what everything else reads.
func (r *RawSet) Get(name string) (RawDataset, bool) {
	if r == nil {
		return RawDataset{}, false
	}
	dataset, ok := r.datasets[name]
	if !ok {
		return RawDataset{}, false
	}

	return dataset.Clone(), true
}

// Has reports whether the set holds the named dataset.
func (r *RawSet) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.datasets[name]

	return ok
}
