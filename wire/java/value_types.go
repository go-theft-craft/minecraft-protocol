package java

// UUID is a Java Edition UUID in network byte order.
type UUID [16]byte

// Position is a packed Java Edition block position.
type Position struct {
	X int
	Y int
	Z int
}

// Slot is a Java 1.8 item stack. An absent slot has Present set to false.
type Slot struct {
	Present bool
	BlockID int16
	Count   int8
	Damage  int16
	NBT     *NBT
}

// EntityMetadataType identifies the value stored in one Java 1.8 metadata entry.
type EntityMetadataType uint8

const (
	// MetadataByte stores int8.
	MetadataByte EntityMetadataType = iota
	// MetadataShort stores int16.
	MetadataShort
	// MetadataInt stores int32.
	MetadataInt
	// MetadataFloat stores float32.
	MetadataFloat
	// MetadataString stores string.
	MetadataString
	// MetadataSlot stores Slot.
	MetadataSlot
	// MetadataPosition stores MetadataCoordinates.
	MetadataPosition
	// MetadataRotation stores Rotation.
	MetadataRotation
)

// MetadataCoordinates is the three-integer position used by entity metadata.
type MetadataCoordinates struct {
	X int32
	Y int32
	Z int32
}

// Rotation is the three-float rotation used by entity metadata.
type Rotation struct {
	Pitch float32
	Yaw   float32
	Roll  float32
}

// EntityMetadataEntry retains one typed metadata value and its wire index.
// Value contains int8, int16, int32, float32, string, Slot,
// MetadataCoordinates, or Rotation according to Type.
type EntityMetadataEntry struct {
	Index uint8
	Type  EntityMetadataType
	Value any
}

// EntityMetadata retains entries in wire order.
type EntityMetadata []EntityMetadataEntry
