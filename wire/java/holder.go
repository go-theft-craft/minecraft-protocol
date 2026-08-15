package java

import "fmt"

// Holder is either a reference into a server-sent registry or an inline value.
//
// The wire form is one VarInt. Zero means an inline value follows; anything
// else is a registry ID, biased by one so that zero stays free. The bias is the
// whole reason this type exists rather than an option: ID 0 is a real registry
// entry, and an unbiased encoding could not distinguish it from "inline".
type Holder[T any] struct {
	// ID is the registry entry, valid when Inline is nil.
	ID int32
	// Inline is the value carried in the packet itself.
	Inline *T
}

// NewHolderID returns a Holder naming a registry entry.
func NewHolderID[T any](id int32) Holder[T] { return Holder[T]{ID: id} }

// NewHolderInline returns a Holder carrying a value.
func NewHolderInline[T any](value T) Holder[T] { return Holder[T]{Inline: &value} }

// HolderSet is either a named tag or an explicit list of registry entries.
//
// The wire form is one VarInt. Zero means a tag name follows; anything else is
// one more than the number of entries, so an empty explicit set is encodable
// and distinct from a tag.
type HolderSet[T any] struct {
	// Tag names a registry tag, valid when IDs is nil.
	Tag string
	// IDs are the explicit entries. A non-nil empty slice is an empty set.
	IDs []T
}

// NewHolderSetTag returns a HolderSet naming a tag.
func NewHolderSetTag[T any](tag string) HolderSet[T] { return HolderSet[T]{Tag: tag} }

// NewHolderSetIDs returns a HolderSet listing entries explicitly.
func NewHolderSetIDs[T any](ids []T) HolderSet[T] {
	if ids == nil {
		ids = []T{}
	}

	return HolderSet[T]{IDs: ids}
}

// ReadHolder reads the discriminator and reports whether an inline value
// follows. When it does not, the returned ID is the registry entry.
//
// The generated codec calls this, then reads the inline value itself, because
// only the generated code knows how to decode T.
func (b *Buffer) ReadHolder(path string) (id int32, inline bool, err error) {
	raw, err := b.ReadVarInt(path)
	if err != nil {
		return 0, false, err
	}
	if raw == 0 {
		return 0, true, nil
	}
	if raw < 0 {
		return 0, false, withPath(path, fmt.Errorf("%w: holder discriminator %d is negative", ErrValueOutOfRange, raw))
	}

	return raw - 1, false, nil
}

// WriteHolder writes the discriminator for a registry reference or an inline
// value. The generated codec writes the inline value afterwards.
func (b *Buffer) WriteHolder(path string, id int32, inline bool) error {
	if inline {
		return b.WriteVarInt(path, 0)
	}
	if id < 0 {
		return withPath(path, fmt.Errorf("%w: holder ID %d is negative", ErrValueOutOfRange, id))
	}
	if id > maxHolderID {
		return withPath(path, fmt.Errorf("%w: holder ID %d cannot be biased", ErrValueOutOfRange, id))
	}

	return b.WriteVarInt(path, id+1)
}

// ReadHolderSet reads the discriminator. It reports whether a tag name follows;
// when it does not, count is how many entries follow.
func (b *Buffer) ReadHolderSet(path string) (count int, tagged bool, err error) {
	raw, err := b.ReadVarInt(path)
	if err != nil {
		return 0, false, err
	}
	if raw == 0 {
		return 0, true, nil
	}
	if raw < 0 {
		return 0, false, withPath(path, fmt.Errorf("%w: holder set discriminator %d is negative", ErrValueOutOfRange, raw))
	}

	// The count is bounded before the caller allocates for it.
	if err := b.ValidateCollection(path, int(raw-1)); err != nil {
		return 0, false, err
	}

	return int(raw - 1), false, nil
}

// WriteHolderSet writes the discriminator for a tag or an explicit set.
func (b *Buffer) WriteHolderSet(path string, count int, tagged bool) error {
	if tagged {
		return b.WriteVarInt(path, 0)
	}
	if err := b.ValidateCollection(path, count); err != nil {
		return err
	}

	return b.WriteVarInt(path, int32(count)+1)
}

// maxHolderID is the largest registry ID the biased encoding can represent.
// One more would overflow the bias rather than encode.
const maxHolderID = int32(^uint32(0)>>1) - 1
