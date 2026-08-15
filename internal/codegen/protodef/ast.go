// Package protodef parses the ProtoDef schema used by Minecraft protocol data.
package protodef

// Kind identifies one form of recursive ProtoDef type node.
type Kind string

const (
	// KindAlias is a reference to another named type.
	KindAlias Kind = "alias"
	// KindPrimitive is a direct reference to a native scalar type.
	KindPrimitive Kind = "primitive"
	// KindNative is a native definition or parameterized native invocation.
	KindNative Kind = "native"
	// KindContainer is an ordered list of named or anonymous fields.
	KindContainer Kind = "container"
	// KindArray is a repeated element type.
	KindArray Kind = "array"
	// KindSwitch selects a type using another value.
	KindSwitch Kind = "switch"
	// KindOption is an optional element type.
	KindOption Kind = "option"
	// KindBuffer is a length-delimited byte buffer.
	KindBuffer Kind = "buffer"
	// KindMapper maps wire values to symbolic names.
	KindMapper Kind = "mapper"
	// KindBitField packs ordered integer fields into bits.
	KindBitField Kind = "bitfield"
	// KindBitFlags maps individual bits to boolean names.
	KindBitFlags Kind = "bitflags"
)

// CountKind identifies how an array, buffer, or string obtains its length.
type CountKind string

const (
	// CountFixed stores a literal element count.
	CountFixed CountKind = "fixed"
	// CountReference names a previously decoded container field.
	CountReference CountKind = "reference"
	// CountType names the wire type used to encode a count prefix.
	CountType CountKind = "type"
)

// Schema is a validated ProtoDef document.
type Schema struct {
	Types     map[string]*TypeNode
	TypeNames []string
	States    []State
}

// State contains the protocol directions for one connection state.
type State struct {
	Name       string
	Directions []Direction
}

// Direction contains local definitions and the ordered packet inventory.
type Direction struct {
	Name         string
	Types        map[string]*TypeNode
	TypeNames    []string
	PacketMap    *TypeNode
	PacketSwitch *TypeNode
	Packets      []Packet
}

// Packet describes one numeric packet ID, its symbolic name, and its body type.
type Packet struct {
	ID       int
	IDKey    string
	Name     string
	TypeName string
	Type     *TypeNode
}

// TypeNode is a recursive tagged representation of a ProtoDef type.
// Fields that do not apply to Kind are left at their zero value.
type TypeNode struct {
	Kind      Kind
	Name      string
	Target    *TypeNode
	Arguments []Argument

	Fields  []Field
	Element *TypeNode
	Count   *Count

	CompareTo string
	Cases     []SwitchCase
	Default   *TypeNode

	Mappings []Mapping
	Bits     []BitField
	Flags    []string

	path       string
	definition bool
}

// Argument retains a named parameter supplied to an alias or native type.
type Argument struct {
	Name   string
	String string
	Number string
	Bool   *bool
	Type   *TypeNode
	Raw    []byte
	// FieldName is set when the argument is a named field rather than a bare
	// type: protocol 775's registry holders pass {"name": ..., "type": ...} so
	// the decoded value has somewhere to go. The name matters because it is
	// what upstream calls the field, and a differential comparison needs it.
	FieldName string
}

// Field is one ordered container member.
type Field struct {
	Name      string
	Anonymous bool
	Type      *TypeNode
}

// Count describes a fixed length, a field reference, or a prefixed count type.
type Count struct {
	Kind      CountKind
	Fixed     int
	Reference string
	Type      *TypeNode
}

// SwitchCase retains a ProtoDef switch key exactly as written in the source.
type SwitchCase struct {
	Key  string
	Type *TypeNode
}

// Mapping retains one mapper key and its symbolic value.
type Mapping struct {
	Key   string
	Value string

	path string
}

// BitField describes one ordered segment of a bitfield.
type BitField struct {
	Name   string
	Size   int
	Signed bool
}
