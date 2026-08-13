package data

import "slices"

// WindowID identifies a Minecraft inventory window.
type WindowID string

// Window describes a Minecraft inventory window.
type Window struct {
	ID         WindowID
	Name       string
	Slots      []WindowSlot
	Properties []string
	OpenedWith []WindowOpener
}

// Windows is a collection of Minecraft inventory windows.
type Windows []Window

// Clone returns windows whose mutable fields do not alias the source.
func (w Windows) Clone() Windows {
	if w == nil {
		return nil
	}

	clone := make(Windows, len(w))
	for index := range clone {
		clone[index] = w[index].Clone()
	}

	return clone
}

// WindowSlot describes a slot in a Minecraft inventory window.
type WindowSlot struct {
	Name  string
	Index int
	Size  int
}

// WindowOpener describes an object that opens a Minecraft inventory window.
type WindowOpener struct {
	Type string
	ID   int
}

// Clone returns a Window whose mutable fields do not alias the source.
func (w Window) Clone() Window {
	clone := w
	clone.Slots = slices.Clone(w.Slots)
	clone.Properties = slices.Clone(w.Properties)
	clone.OpenedWith = slices.Clone(w.OpenedWith)

	return clone
}
