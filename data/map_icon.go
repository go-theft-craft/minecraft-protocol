package data

import "slices"

// MapIconID identifies a marker drawn on a filled map.
type MapIconID int

// MapIcon describes one marker a filled map can carry.
type MapIcon struct {
	ID   MapIconID
	Name string
	// Appearance is upstream's prose description of the marker, such as
	// "White marker". It is documentation, not a rendering instruction.
	Appearance         string
	VisibleInItemFrame bool
}

// MapIcons is a collection of filled-map markers.
type MapIcons []MapIcon

// Clone returns map icons that do not alias the source.
func (m MapIcons) Clone() MapIcons { return slices.Clone(m) }
