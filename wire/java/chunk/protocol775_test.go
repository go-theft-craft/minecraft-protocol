package chunk

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

// The fixture is one chunk column as a real Paper 26.1 server sent it, taken
// from the capture behind this repository's vanilla-client check. It is what
// this decoder was blocked on: the section format is not described by any data
// this repository generates from, and a decoder written from memory returns
// plausible wrong blocks rather than an error.
//
// It is terrain and nothing else -- it carries no block entities.
const columnFixture775 = "testdata/chunk-26.1-0-0.bin"

// overworldBottom775 is the lowest section index of a 26.1 overworld, whose
// minimum build height is -64. The blob does not carry it; see Split775.
const overworldBottom775 = -4

func realColumn775(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(columnFixture775)
	if err != nil {
		t.Fatalf("read the captured column: %v", err)
	}

	return data
}

func TestARealColumnSplitsIntoItsSections(t *testing.T) {
	t.Parallel()

	sections, err := Split775(realColumn775(t), overworldBottom775)
	if err != nil {
		t.Fatalf("Split775: %v", err)
	}

	// A 26.1 overworld is 384 blocks tall from y=-64, which is 24 sections.
	if len(sections) != 24 {
		t.Fatalf("the column split into %d sections, want 24", len(sections))
	}
	if sections[0].Y != -4 {
		t.Errorf("the lowest section is at index %d, want -4", sections[0].Y)
	}
	if sections[len(sections)-1].Y != 19 {
		t.Errorf("the highest section is at index %d, want 19", sections[len(sections)-1].Y)
	}
}

// TestEveryRealSectionDecodesToTheBlockCountTheServerDeclared is the check
// that makes this a decoder rather than a guess.
//
// Each section is prefixed with the count of blocks the server considers
// non-empty. Nothing in the decode path reads it -- it is a block semantic,
// and this package does not own those -- so it is an independent statement of
// what the section holds, written by the server that packed the bytes. A
// decoder that misreads the bit width, the palette, or the long packing
// produces a different count. All 24 agreeing is not a decoder that runs; it
// is a decoder that agrees with the server about every one of 98,304 blocks.
//
// The comparison treats state 0 as the only empty one. Vanilla counts three
// air states, so a section holding cave_air or void_air would disagree without
// being wrong; this fixture holds neither, which is checked below.
func TestEveryRealSectionDecodesToTheBlockCountTheServerDeclared(t *testing.T) {
	t.Parallel()

	sections, err := Split775(realColumn775(t), overworldBottom775)
	if err != nil {
		t.Fatalf("Split775: %v", err)
	}

	const voidAir, caveAir = 15292, 15293
	for _, section := range sections {
		states, err := DecodeSection775(section.Blocks)
		if err != nil {
			t.Fatalf("section %d: %v", section.Y, err)
		}
		if len(states) != BlocksPerSection {
			t.Fatalf("section %d decoded %d states, want %d",
				section.Y, len(states), BlocksPerSection)
		}

		filled := 0
		for _, state := range states {
			if state == voidAir || state == caveAir {
				t.Fatalf("section %d holds an air state the count treats as empty", section.Y)
			}
			if state != 0 {
				filled++
			}
		}
		if filled != int(section.BlockCount) {
			t.Errorf("section %d decoded %d filled blocks, the server declared %d",
				section.Y, filled, section.BlockCount)
		}
	}
}

func TestARealColumnHoldsTerrainAtTheBottomAndAirAtTheTop(t *testing.T) {
	t.Parallel()

	// One value everywhere would satisfy the count check for an empty section,
	// so this is the other half: the column is not uniform. Section -4 is the
	// bottom of the world and is full -- bedrock and deepslate -- and section
	// 19 is y=304..319, which is sky.
	sections, err := Split775(realColumn775(t), overworldBottom775)
	if err != nil {
		t.Fatalf("Split775: %v", err)
	}

	bottom, err := DecodeSection775(sections[0].Blocks)
	if err != nil {
		t.Fatalf("decode the lowest section: %v", err)
	}
	if bottom[0] == 0 {
		t.Error("the block at the bottom of the world is air")
	}

	top, err := DecodeSection775(sections[len(sections)-1].Blocks)
	if err != nil {
		t.Fatalf("decode the highest section: %v", err)
	}
	for _, state := range top {
		if state != 0 {
			t.Fatalf("the section at y=304 holds state %d, want air", state)
		}
	}
}

func TestEveryRealSectionCarriesItsBiomes(t *testing.T) {
	t.Parallel()

	// The biome container is walked whether or not a caller wants it, because
	// it sits between one section's blocks and the next one's. Keeping it is
	// what turns that walk into something a caller can read.
	sections, err := Split775(realColumn775(t), overworldBottom775)
	if err != nil {
		t.Fatalf("Split775: %v", err)
	}

	for _, section := range sections {
		biomes, err := DecodeBiomes775(section.Biomes)
		if err != nil {
			t.Fatalf("section %d: %v", section.Y, err)
		}
		if len(biomes) != BiomesPerSection775 {
			t.Fatalf("section %d decoded %d biomes, want %d",
				section.Y, len(biomes), BiomesPerSection775)
		}
	}
}

func TestASectionOfOneValueNeedsNoLongArray(t *testing.T) {
	t.Parallel()

	// The single-valued container is the one the real column uses for every
	// empty section, and it is the case where a decoder that insists on
	// reading a long array walks off the end.
	states, err := DecodeSection775([]byte{0x00, 0x2a})
	if err != nil {
		t.Fatalf("DecodeSection775: %v", err)
	}
	if len(states) != BlocksPerSection {
		t.Fatalf("decoded %d states, want %d", len(states), BlocksPerSection)
	}
	for i, state := range states {
		if state != 42 {
			t.Fatalf("state %d is %d, want 42 everywhere", i, state)
		}
	}
}

func TestAColumnThatDoesNotFitIsRefused(t *testing.T) {
	t.Parallel()

	// A blob that ends mid-section is the shape every misread layout takes,
	// and the failure it must not have is a silent one: sections that decode
	// to plausible wrong blocks. The exact-fit check is what turns it into an
	// error.
	data := realColumn775(t)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "truncated", data: data[:len(data)-1]},
		{name: "trailing byte", data: append(append([]byte{}, data...), 0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Split775(test.data, overworldBottom775); !errors.Is(err, ErrColumn) {
				t.Errorf("got %v, want ErrColumn", err)
			}
		})
	}
}

func TestADecodedSectionMatchesItsPackedEntries(t *testing.T) {
	t.Parallel()

	// A hand-packed container, so the bit arithmetic is checked against
	// something whose answer is known independently of the fixture: five bits
	// an entry, twelve entries a long.
	palette := []uint32{0, 11, 22, 33}
	raw := packedContainer(5, palette, BlocksPerSection)

	states, err := DecodeSection775(raw)
	if err != nil {
		t.Fatalf("DecodeSection775: %v", err)
	}
	for i, state := range states {
		if want := palette[i%len(palette)]; state != want {
			t.Fatalf("state %d is %d, want %d", i, state, want)
		}
	}
}

// packedContainer builds one indirect-palette container whose entries cycle
// through the palette, which is the layout vanilla's SimpleBitStorage writes:
// entries packed whole into each long, low bits first, and never straddling.
func packedContainer(width int, palette []uint32, entries int) []byte {
	raw := []byte{byte(width), byte(len(palette))}
	for _, entry := range palette {
		raw = append(raw, byte(entry))
	}

	perLong := 64 / width
	longs := make([]byte, longsFor(entries, perLong)*8)
	for i := range entries {
		cell := i / perLong
		word := binary.BigEndian.Uint64(longs[cell*8:])
		word |= uint64(i%len(palette)) << ((i - cell*perLong) * width)
		binary.BigEndian.PutUint64(longs[cell*8:], word)
	}

	return append(raw, longs...)
}

func TestAnUndecodableSectionReportsItself(t *testing.T) {
	t.Parallel()

	// Short of its long array: a section a caller cannot decode has to say so
	// rather than answer with zeros, because zero is air and air is an answer
	// a consumer will walk into.
	if _, err := DecodeSection775([]byte{0x04, 0x01, 0x01, 0x00}); !errors.Is(err, ErrSection) {
		t.Errorf("got %v, want ErrSection", err)
	}
}

func TestAnEntryOutsideItsPaletteIsRefused(t *testing.T) {
	t.Parallel()

	// One bit an entry and a palette of one: every stored 1 indexes past the
	// end. Answering with whatever is at that index, or with zero, would be a
	// block this server never sent.
	raw := packedContainer(1, []uint32{7}, BlocksPerSection)
	// packedContainer cycles through the palette, so a palette of one stores
	// only zeros. Set the first entry to 1 by hand.
	raw[len(raw)-8] = 0x00
	raw[len(raw)-1] = 0x01

	if _, err := DecodeSection775(raw); !errors.Is(err, ErrSection) {
		t.Errorf("got %v, want ErrSection", err)
	}
}

// TestAnImpossibleBitWidthIsRefusedRatherThanDividingByZero pins the guard.
//
// A width past 64 makes the entries-per-long arithmetic zero, and dividing by
// it panics. A malformed column arrives from the network, so a panic there is
// a remote crash rather than a decode failure; both the walk and the decode
// have to refuse it.
func TestAnImpossibleBitWidthIsRefusedRatherThanDividingByZero(t *testing.T) {
	t.Parallel()

	if _, err := DecodeSection775([]byte{0xFF, 0x00}); !errors.Is(err, ErrSection) {
		t.Errorf("decode: got %v, want ErrSection", err)
	}

	// A section header, then the same impossible width in its block container.
	column := []byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0x00}
	if _, err := Split775(column, 0); !errors.Is(err, ErrColumn) {
		t.Errorf("split: got %v, want ErrColumn", err)
	}
}

func TestAColumnOfEndlessSectionsIsBounded(t *testing.T) {
	t.Parallel()

	// Eight bytes an empty section -- two counts and two single-valued
	// containers -- so a blob a caller might accept as a column describes
	// thousands of them. Nothing in the blob says how many there should be,
	// which is why the bound is here rather than in the caller.
	var column []byte
	for range sectionsPerColumnLimit + 1 {
		column = append(column, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
	}

	if _, err := Split775(column, 0); !errors.Is(err, ErrColumn) {
		t.Errorf("got %v, want ErrColumn", err)
	}
}
