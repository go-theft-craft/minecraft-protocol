package chunk

import (
	"bytes"
	"errors"
	"testing"
)

// blockLightMark and skyLightMark fill the two light arrays of a built column
// with different bytes, so a reader that swaps them, or that reads sky light a
// dimension does not send, is caught by what it returns rather than only by a
// length.
const (
	blockLightMark = 0xB1
	skyLightMark   = 0x5C
	biomeMark      = 0xB0
)

// buildColumn47 lays out a column the way a 1.8 server writes one: every
// present section's blocks, then every present section's block light, then the
// same for sky light if the dimension has it, then the biome map if the packet
// is ground-up. Each section's blocks are filled with its own index, so a
// misaligned split shows up as the wrong section rather than as no error.
func buildColumn47(layout Layout47) []byte {
	var column []byte
	for y := range sectionsPerColumn47 {
		if layout.Bitmap&(1<<uint(y)) == 0 {
			continue
		}
		column = append(column, bytes.Repeat([]byte{byte(y)}, SectionBlockBytes47)...)
	}
	for range layout.Sections() {
		column = append(column, bytes.Repeat([]byte{blockLightMark}, SectionLightBytes47)...)
	}
	if layout.SkyLight {
		for range layout.Sections() {
			column = append(column, bytes.Repeat([]byte{skyLightMark}, SectionLightBytes47)...)
		}
	}
	if layout.GroundUp {
		column = append(column, bytes.Repeat([]byte{biomeMark}, BiomeBytes47)...)
	}

	return column
}

func TestSplit47SlicesOnlyThePresentSections(t *testing.T) {
	t.Parallel()

	// A bitmask with a gap, because a column of consecutive sections cannot
	// tell a reader that walks the mask from one that walks the blob.
	layout := Layout47{Bitmap: 0b0000_0000_0000_0101, SkyLight: true, GroundUp: true}
	sections, rest, err := Split47(layout.Bitmap, buildColumn47(layout))
	if err != nil {
		t.Fatalf("Split47: %v", err)
	}

	if len(sections) != 2 {
		t.Fatalf("the column split into %d sections, want 2", len(sections))
	}
	for i, want := range []int{0, 2} {
		if sections[i].Y != want {
			t.Errorf("section %d is at index %d, want %d", i, sections[i].Y, want)
		}
		if len(sections[i].Blocks) != SectionBlockBytes47 {
			t.Fatalf("section %d holds %d bytes, want %d",
				sections[i].Y, len(sections[i].Blocks), SectionBlockBytes47)
		}
		// The fill is the section's own index, so this says the slice came
		// from where the mask says it did.
		if got := sections[i].Blocks[0]; got != byte(want) {
			t.Errorf("section %d holds section %d's bytes", want, got)
		}
	}

	// Light and biomes, untouched: 2 sections of block light, 2 of sky light,
	// and the biome map.
	if want := 2*2*SectionLightBytes47 + BiomeBytes47; len(rest) != want {
		t.Errorf("%d bytes left after the blocks, want %d", len(rest), want)
	}
}

func TestSplit47KeepsTheSectionsThatArrivedWhenTheColumnIsShort(t *testing.T) {
	t.Parallel()

	// A column cut inside its second section. What arrived is still what
	// arrived, and a caller that would rather keep it than drop the packet
	// gets it alongside the error.
	layout := Layout47{Bitmap: 0b11}
	column := buildColumn47(layout)
	sections, _, err := Split47(layout.Bitmap, column[:SectionBlockBytes47+100])

	if !errors.Is(err, ErrColumn) {
		t.Errorf("got %v, want ErrColumn", err)
	}
	if len(sections) != 1 {
		t.Fatalf("kept %d sections, want the 1 that arrived whole", len(sections))
	}
	if sections[0].Y != 0 {
		t.Errorf("kept section %d, want section 0", sections[0].Y)
	}
}

func TestDecode47ReadsTheLightAndBiomesBehindTheBlocks(t *testing.T) {
	t.Parallel()

	layout := Layout47{Bitmap: 0b0000_0000_0000_1011, SkyLight: true, GroundUp: true}
	column, err := Decode47(layout, buildColumn47(layout))
	if err != nil {
		t.Fatalf("Decode47: %v", err)
	}

	if len(column.Sections) != 3 {
		t.Fatalf("the column holds %d sections, want 3", len(column.Sections))
	}
	if len(column.BlockLight) != 3 || len(column.SkyLight) != 3 {
		t.Fatalf("the column holds %d block light and %d sky light arrays, want 3 of each",
			len(column.BlockLight), len(column.SkyLight))
	}
	// Read in the order the server wrote them: all the block light, then all
	// the sky light. Reading one where the other is expected is a silent swap
	// of two arrays of the same length.
	for i := range column.Sections {
		if column.BlockLight[i][0] != blockLightMark {
			t.Errorf("section %d's block light starts with %#x",
				column.Sections[i].Y, column.BlockLight[i][0])
		}
		if column.SkyLight[i][0] != skyLightMark {
			t.Errorf("section %d's sky light starts with %#x",
				column.Sections[i].Y, column.SkyLight[i][0])
		}
	}
	if len(column.Biomes) != BiomeBytes47 || column.Biomes[0] != biomeMark {
		t.Errorf("the column's biome map is %d bytes starting with %#x",
			len(column.Biomes), column.Biomes[0])
	}
}

func TestDecode47SendsNoSkyLightWhereTheDimensionHasNone(t *testing.T) {
	t.Parallel()

	layout := Layout47{Bitmap: 0b1, GroundUp: true}
	column, err := Decode47(layout, buildColumn47(layout))
	if err != nil {
		t.Fatalf("Decode47: %v", err)
	}
	if column.SkyLight != nil {
		t.Errorf("a nether column decoded %d sky light arrays", len(column.SkyLight))
	}
	if column.BlockLight[0][0] != blockLightMark {
		t.Error("the block light of a column with no sky light is not the block light")
	}
}

// TestDecode47RefusesAColumnThatIsNotTheLayoutItWasGiven is the check that
// catches the two conditions the blob does not carry.
//
// Neither wrong setting damages the block data -- that is first, and it is the
// same either way -- so nothing before the end of the column disagrees. The
// length is what disagrees, and it is the only thing that does.
func TestDecode47RefusesAColumnThatIsNotTheLayoutItWasGiven(t *testing.T) {
	t.Parallel()

	sent := Layout47{Bitmap: 0b111, SkyLight: true, GroundUp: true}
	column := buildColumn47(sent)

	for name, read := range map[string]Layout47{
		"no sky light":  {Bitmap: 0b111, GroundUp: true},
		"not ground up": {Bitmap: 0b111, SkyLight: true},
		"wrong bitmask": {Bitmap: 0b11, SkyLight: true, GroundUp: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Decode47(read, column); !errors.Is(err, ErrColumn) {
				t.Errorf("got %v, want ErrColumn", err)
			}
		})
	}
}

// TestLayout47BytesIsTheStrideTheBulkPacketNeeds pins the arithmetic that
// slices the bulk packet, which concatenates columns with no lengths of their
// own: one wrong stride misaligns every column after it.
func TestLayout47BytesIsTheStrideTheBulkPacketNeeds(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		layout Layout47
		want   int
	}{
		// 16 sections of 8192 blocks, 2048 block light and 2048 sky light,
		// and 256 biomes.
		"a full overworld column": {
			layout: Layout47{Bitmap: 0xFFFF, SkyLight: true, GroundUp: true},
			want:   16*(8192+2048+2048) + 256,
		},
		"a full nether column": {
			layout: Layout47{Bitmap: 0xFFFF, GroundUp: true},
			want:   16*(8192+2048) + 256,
		},
		"one section, not ground up": {
			layout: Layout47{Bitmap: 0b1, SkyLight: true},
			want:   8192 + 2048 + 2048,
		},
		// No sections at all. A ground-up column of this shape is protocol
		// 47's unload, and vanilla still writes the biome map for it; see
		// Decode47 for why a caller should recognise the unload from the
		// packet rather than from a length.
		"no sections": {
			layout: Layout47{Bitmap: 0, GroundUp: true},
			want:   256,
		},
		"no sections, not ground up": {
			layout: Layout47{Bitmap: 0},
			want:   0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := test.layout.Bytes(); got != test.want {
				t.Errorf("Bytes() is %d, want %d", got, test.want)
			}
			if got := len(buildColumn47(test.layout)); got != test.want {
				t.Errorf("a built column is %d bytes, want %d", got, test.want)
			}
		})
	}
}

func TestDecodeSection47ReadsEachBlockLittleEndian(t *testing.T) {
	t.Parallel()

	// The state is the block ID shifted four bits with the metadata in the low
	// nibble, written low byte first. Stone with metadata 3 is 1<<4|3 = 19,
	// and the ID that needs both bytes is what a reader of only the first one
	// gets wrong: 175<<4 = 2800, which is 0xAF0.
	raw := make([]byte, SectionBlockBytes47)
	raw[0], raw[1] = 0x13, 0x00
	raw[2], raw[3] = 0xF0, 0x0A

	states, err := DecodeSection47(raw)
	if err != nil {
		t.Fatalf("DecodeSection47: %v", err)
	}
	if len(states) != BlocksPerSection {
		t.Fatalf("decoded %d states, want %d", len(states), BlocksPerSection)
	}
	if states[0] != 19 {
		t.Errorf("the first block is state %d, want 19", states[0])
	}
	if states[1] != 2800 {
		t.Errorf("the second block is state %d, want 2800", states[1])
	}
	for i, state := range states[2:] {
		if state != 0 {
			t.Fatalf("block %d is state %d, want air", i+2, state)
		}
	}
}

func TestASectionShorterThanItsBlocksIsRefused(t *testing.T) {
	t.Parallel()

	// Zero is air, and air is an answer a consumer will walk into, so a
	// section that cannot be read has to say so rather than pad itself.
	if _, err := DecodeSection47(make([]byte, SectionBlockBytes47-1)); !errors.Is(err, ErrSection) {
		t.Errorf("got %v, want ErrSection", err)
	}
}
