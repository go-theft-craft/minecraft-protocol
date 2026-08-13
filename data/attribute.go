package data

import "slices"

// Attribute describes a Minecraft entity attribute.
type Attribute struct {
	Name     string
	Resource string
	Default  float64
	Min      float64
	Max      float64
}

// Attributes is a collection of Minecraft entity attributes.
type Attributes []Attribute

// Clone returns attributes that do not alias the source.
func (a Attributes) Clone() Attributes { return slices.Clone(a) }
