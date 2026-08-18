package chunk

import (
	"encoding/binary"
	"fmt"
)

// Protocol 775's column is self-describing except for where it starts. Every
// section is written the same way and they simply follow one another until the
// blob runs out:
//
//	section   := nonEmptyBlockCount:i16 fluidCount:i16 states biomes
//	container := bitsPerEntry:u8 palette longs:i64[derived]
//
// This is LevelChunkSection.write and PalettedContainer.Data.write, and it is
// not the layout a reader of earlier versions would write down. A section
// carries two counts rather than one, and the long array carries no count at
// all -- vanilla writes it with writeFixedSizeLongArray -- so its length is
// derived from the bit width and getting it wrong silently misaligns every
// section after it.
//
// The palette depends on the bit width, and the widths differ between the two
// containers because they are built by different strategies
// (Strategy.createForBlockStates and createForBiomes):
//
//	0                  one VarInt: every entry is that value, and no longs
//	1..8 states        a VarInt count and that many VarInts
//	1..3 biomes        the same, at the narrower biome widths
//	anything wider     the global palette: nothing on the wire at all
//
// Entries are packed whole into each long and never straddle one, so a long
// holds 64/bits of them and the remainder costs a whole long.
const (
	// BiomesPerSection775 is the biome count of one section: biomes are stored
	// at four-block resolution, so 4x4x4 of them.
	BiomesPerSection775 = 64
	// MaxBlockPaletteBits775 and MaxBiomePaletteBits775 are the widest bit
	// widths that still carry a palette. Wider means the global palette, whose
	// entries are the registry's own IDs.
	MaxBlockPaletteBits775 = 8
	MaxBiomePaletteBits775 = 3
	// maxPackedBits is the widest bit width this reads at all. Vanilla's
	// widest is fifteen. The limit is what keeps a malformed width from
	// dividing by zero rather than failing.
	maxPackedBits = 32
	// sectionsPerColumnLimit bounds a column before its section count is
	// known, so a malformed blob cannot be read as millions of sections. No
	// vanilla dimension is close: the overworld is 24.
	sectionsPerColumnLimit = 128
)

// Split775 walks a column into its sections.
//
// bottom is the index of the column's lowest section, which is the dimension's
// minimum build height divided by sixteen -- minus four in a 26.1 overworld.
// The blob does not carry it, and it is not derivable from anything in the
// chunk packet: it comes from the dimension_type registry sent in
// configuration. A caller that assumes zero puts every block of an overworld
// column sixty-four blocks too high, which reads as a working client.
//
// Both containers of every section are walked, including the biome container
// no caller may want, because it sits between one section's blocks and the
// next one's.
func Split775(data []byte, bottom int) ([]Section, error) {
	r := &reader{data: data}

	var sections []Section
	for index := 0; r.pos < len(data); index++ {
		if index >= sectionsPerColumnLimit {
			return nil, fmt.Errorf(
				"%w: more than %d sections", ErrColumn, sectionsPerColumnLimit,
			)
		}

		blockCount, err := r.short()
		if err != nil {
			return nil, err
		}
		fluidCount, err := r.short()
		if err != nil {
			return nil, err
		}
		blocks, err := r.container(BlocksPerSection, MaxBlockPaletteBits775)
		if err != nil {
			return nil, err
		}
		biomes, err := r.container(BiomesPerSection775, MaxBiomePaletteBits775)
		if err != nil {
			return nil, err
		}

		sections = append(sections, Section{
			Y:          bottom + index,
			Blocks:     blocks,
			Biomes:     biomes,
			BlockCount: blockCount,
			FluidCount: fluidCount,
		})
	}

	// The loop stops when the blob runs out, so it always ends on a boundary
	// it believes in. What proves the belief is that the last section ended
	// exactly at the end: a misread bit width or a missed field leaves the
	// cursor short or past, and this is the only signal that says so before
	// the wrong blocks reach a consumer.
	if r.pos != len(data) {
		return nil, fmt.Errorf("%w: read %d of %d bytes", ErrColumn, r.pos, len(data))
	}

	return sections, nil
}

// DecodeSection775 turns one section's block-state container into 4096 block
// states, indexed y*256 + z*16 + x.
//
// The states are the registry IDs the server's own block registry assigns,
// which is what block_update and section_blocks_update send too.
//
// It is pure. Two callers decoding the same bytes compute the same answer,
// which is what lets a consumer decode a section lazily and without a lock.
func DecodeSection775(raw []byte) ([]uint32, error) {
	return decodeContainer(raw, BlocksPerSection, MaxBlockPaletteBits775)
}

// DecodeBiomes775 turns one section's biome container into 64 biome registry
// IDs, indexed y*16 + z*4 + x over the section's 4x4x4 biome cells.
func DecodeBiomes775(raw []byte) ([]uint32, error) {
	return decodeContainer(raw, BiomesPerSection775, MaxBiomePaletteBits775)
}

// decodeContainer unpacks one paletted container into one value per entry.
func decodeContainer(raw []byte, entries, maxPaletteBits int) ([]uint32, error) {
	r := &reader{data: raw}

	width, err := r.byteValue()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSection, err)
	}
	if int(width) > maxPackedBits {
		return nil, fmt.Errorf("%w: %d bits an entry", ErrSection, width)
	}

	palette, err := readPalette(r, int(width), maxPaletteBits)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSection, err)
	}

	values := make([]uint32, entries)
	// A single-valued container has no long array at all, because there is
	// nothing to distinguish: every entry is that one value.
	if width == 0 {
		for i := range values {
			values[i] = palette[0]
		}

		return values, nil
	}

	perLong := 64 / int(width)
	packed, err := r.take(longsFor(entries, perLong) * 8)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSection, err)
	}

	mask := uint64(1)<<width - 1
	for i := range values {
		cell := i / perLong
		word := binary.BigEndian.Uint64(packed[cell*8:])
		value := uint32(word >> ((i - cell*perLong) * int(width)) & mask)
		// An indirect palette maps the stored value to a registry ID; the
		// global palette stores the ID itself.
		if palette != nil {
			if int(value) >= len(palette) {
				return nil, fmt.Errorf(
					"%w: entry %d is %d in a palette of %d", ErrSection, i, value, len(palette),
				)
			}
			value = palette[value]
		}
		values[i] = value
	}

	return values, nil
}

// readPalette reads the palette that follows a bit width, or returns nil for
// the global palette, which writes none.
func readPalette(r *reader, width, maxPaletteBits int) ([]uint32, error) {
	switch {
	case width == 0:
		value, err := r.varint()
		if err != nil {
			return nil, err
		}

		return []uint32{uint32(value)}, nil

	case width <= maxPaletteBits:
		count, err := r.varint()
		if err != nil {
			return nil, err
		}
		if count < 0 || count > 1<<width {
			return nil, fmt.Errorf(
				"%w: a palette of %d at %d bits", ErrColumn, count, width,
			)
		}
		palette := make([]uint32, count)
		for i := range palette {
			value, err := r.varint()
			if err != nil {
				return nil, err
			}
			palette[i] = uint32(value)
		}

		return palette, nil

	default:
		return nil, nil
	}
}

// container walks one paletted container and returns its whole encoding,
// header included, so a decode can read it again without the split having to
// carry the palette alongside the bytes.
func (r *reader) container(entries, maxPaletteBits int) ([]byte, error) {
	start := r.pos

	width, err := r.byteValue()
	if err != nil {
		return nil, err
	}
	if int(width) > maxPackedBits {
		return nil, fmt.Errorf("%w: %d bits an entry", ErrColumn, width)
	}
	if _, err := readPalette(r, int(width), maxPaletteBits); err != nil {
		return nil, err
	}
	if width > 0 {
		if _, err := r.take(longsFor(entries, 64/int(width)) * 8); err != nil {
			return nil, err
		}
	}

	return r.data[start:r.pos], nil
}

// longsFor is the length vanilla's SimpleBitStorage allocates.
func longsFor(entries, perLong int) int { return (entries + perLong - 1) / perLong }
