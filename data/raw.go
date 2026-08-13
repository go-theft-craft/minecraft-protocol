package data

import "errors"

// ErrInvalidDataset reports an empty or duplicate raw dataset name.
var ErrInvalidDataset = errors.New("invalid raw dataset")

// RawDataset preserves an upstream data file that has no typed representation.
type RawDataset struct {
	Name      string
	Path      string
	MediaType string
	Data      []byte
}

// Clone returns a dataset whose bytes do not alias the source.
func (d RawDataset) Clone() RawDataset {
	clone := d
	clone.Data = append([]byte(nil), d.Data...)

	return clone
}
