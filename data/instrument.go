package data

import "slices"

// InstrumentID identifies a Minecraft note-block instrument.
type InstrumentID int

// Instrument describes a Minecraft note-block instrument.
type Instrument struct {
	ID   InstrumentID
	Name string
}

// Instruments is a collection of Minecraft note-block instruments.
type Instruments []Instrument

// Clone returns instruments that do not alias the source.
func (i Instruments) Clone() Instruments { return slices.Clone(i) }
