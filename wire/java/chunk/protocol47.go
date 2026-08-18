package chunk

import (
	"fmt"
	"math/bits"
)

// Protocol 47's column is fixed-width and positional. Every present section
// contributes the same number of bytes, and which sections are present is a
// bitmask on the packet rather than anything in the blob:
//
//	column := blocks[present] blockLight[present] skyLight[present]? biomes?
//	blocks := (blockID<<4 | metadata):u16le[4096]
//
// Two of those are conditional and neither is stated by the blob. Sky light is
// sent only in dimensions that have it, which the chunk packet never says --
// the client knows it from the dimension it is in. Biomes are sent only when
// the packet is ground-up. Both change the stride, so reading a column of one
// shape as the other misaligns everything after the block data, and in the
// bulk packet it misaligns every column that follows as well.
//
// The block data is first, which is the one piece of luck in the layout: a
// caller that wants blocks and nothing else can slice them without knowing
// either condition. That is what Split47 is for.
const (
	// SectionBlockBytes47 is one section's block data: 4096 little-endian
	// sixteen-bit values, each a block ID shifted four bits with its metadata
	// in the low nibble.
	SectionBlockBytes47 = BlocksPerSection * 2
	// SectionLightBytes47 is one section's light data, one nibble per block.
	SectionLightBytes47 = BlocksPerSection / 2
	// BiomeBytes47 is a ground-up column's biome map: one byte per column of
	// blocks, 16x16 of them.
	BiomeBytes47 = 256
	// sectionsPerColumn47 is the height of every protocol 47 dimension, in
	// sections. It is also the width of the bitmask.
	sectionsPerColumn47 = 16
)

// Layout47 is what a protocol 47 column's shape depends on that the column
// itself does not carry.
//
// Bitmap is the packet's own field. SkyLight is a property of the dimension
// the column is in: the overworld sends it, the nether and the end do not.
// GroundUp is the packet's field of that name, true for a whole column and for
// every column in the bulk packet, false for an update to one already sent.
type Layout47 struct {
	Bitmap   uint16
	SkyLight bool
	GroundUp bool
}

// Sections reports how many sections the bitmask marks present.
func (l Layout47) Sections() int { return bits.OnesCount16(l.Bitmap) }

// Bytes reports the length of a column with this layout.
//
// It is the only way to find where one column ends inside the bulk packet,
// which concatenates columns with no lengths of their own.
func (l Layout47) Bytes() int {
	perSection := SectionBlockBytes47 + SectionLightBytes47
	if l.SkyLight {
		perSection += SectionLightBytes47
	}
	total := l.Sections() * perSection
	if l.GroundUp {
		total += BiomeBytes47
	}

	return total
}

// Column47 is one whole protocol 47 column.
//
// BlockLight and SkyLight are indexed alongside Sections, so the light of
// Sections[i] is BlockLight[i]. SkyLight is nil for a dimension that sends
// none, and Biomes is nil for a column that is not ground-up. All of them
// alias the data they were read from.
type Column47 struct {
	Sections   []Section
	BlockLight [][]byte
	SkyLight   [][]byte
	Biomes     []byte
}

// Split47 slices a column into one byte range per present section, and returns
// whatever follows the block data unread.
//
// This is the cheap path, and the one that needs no dimension: the block data
// comes first for every section, so it can be sliced without knowing whether
// sky light is sent or whether the column is ground-up. What follows is
// returned whole rather than dropped, because it is light and biomes and a
// caller that wants those has Decode47.
//
// A column that ends inside a section is short, which is the server's business
// rather than a reason to lose what did arrive: the sections read before the
// end are returned alongside the error.
func Split47(bitmap uint16, data []byte) ([]Section, []byte, error) {
	var sections []Section
	offset := 0
	for y := range sectionsPerColumn47 {
		if bitmap&(1<<uint(y)) == 0 {
			continue
		}
		if offset+SectionBlockBytes47 > len(data) {
			return sections, data[len(data):], fmt.Errorf(
				"%w: section %d needs %d bytes and %d are left",
				ErrColumn, y, SectionBlockBytes47, len(data)-offset,
			)
		}
		sections = append(sections, Section{
			Y:      y,
			Blocks: data[offset : offset+SectionBlockBytes47],
		})
		offset += SectionBlockBytes47
	}

	return sections, data[offset:], nil
}

// Decode47 reads a whole column: its sections, their light, and its biomes.
//
// The layout has to be right. A column read with the wrong sky-light or
// ground-up setting does not fail on its block data -- that part is first and
// is the same either way -- so the exact-fit check at the end is what catches
// it, and it is the reason this refuses a column that is longer than its
// layout as firmly as one that is shorter.
//
// One column is not terrain at all. Protocol 47 has no unload packet: a
// ground-up column with an empty bitmask is how a server says a column is
// gone, and a client recognises it from those two fields without reading the
// payload. Vanilla still writes the biome map behind them and a server that
// knows the client will not look sends nothing, so both lengths occur and
// neither is wrong. Recognise the unload from the packet before decoding.
func Decode47(layout Layout47, data []byte) (*Column47, error) {
	if want := layout.Bytes(); len(data) != want {
		return nil, fmt.Errorf("%w: %d bytes for a column of %d", ErrColumn, len(data), want)
	}

	sections, rest, err := Split47(layout.Bitmap, data)
	if err != nil {
		return nil, err
	}

	column := &Column47{Sections: sections}
	r := &reader{data: rest}

	column.BlockLight, err = readLight47(r, len(sections))
	if err != nil {
		return nil, err
	}
	if layout.SkyLight {
		column.SkyLight, err = readLight47(r, len(sections))
		if err != nil {
			return nil, err
		}
	}
	if layout.GroundUp {
		column.Biomes, err = r.take(BiomeBytes47)
		if err != nil {
			return nil, err
		}
	}

	return column, nil
}

func readLight47(r *reader, sections int) ([][]byte, error) {
	light := make([][]byte, 0, sections)
	for range sections {
		nibbles, err := r.take(SectionLightBytes47)
		if err != nil {
			return nil, err
		}
		light = append(light, nibbles)
	}

	return light, nil
}

// DecodeSection47 turns one section's block data into 4096 block states,
// indexed y*256 + z*16 + x.
//
// The state is the block ID shifted four bits with the metadata in the low
// nibble, which is the identifier protocol 47 uses everywhere else -- in
// block_change and in multi_block_change -- so a caller can compare what it
// decoded here against what a later packet sends without converting either.
//
// It is pure. Two callers decoding the same bytes compute the same answer,
// which is what lets a consumer decode a section lazily and without a lock.
func DecodeSection47(raw []byte) ([]uint32, error) {
	if len(raw) < SectionBlockBytes47 {
		return nil, fmt.Errorf(
			"%w: %d bytes for a section of %d", ErrSection, len(raw), SectionBlockBytes47,
		)
	}

	states := make([]uint32, BlocksPerSection)
	for i := range states {
		states[i] = uint32(raw[i*2]) | uint32(raw[i*2+1])<<8
	}

	return states, nil
}
