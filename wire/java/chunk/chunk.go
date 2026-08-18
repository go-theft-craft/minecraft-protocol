// Package chunk reads the chunk column that Java Edition's chunk packets carry
// as one opaque byte array.
//
// The generated codecs stop at that array deliberately. ProtoDef describes the
// field as a buffer and nothing more, because what is inside it is a property
// of the game version rather than of the schema: the pinned upstream data this
// repository generates from does not describe a section, a palette, or a light
// array. So the layout is written down here instead, once, rather than in
// every client and server that needs a block out of a column.
//
// Two protocols and two layouts, sharing nothing. Protocol 47 packs fixed
// sixteen-bit blocks and derives everything from a section bitmask; protocol
// 775 packs paletted containers whose widths change per section. The functions
// are named for the protocol they read for the same reason NBT and NetworkNBT
// are separate types: a blob of one read as the other decodes to plausible
// wrong blocks rather than to an error.
//
// Splitting is separate from decoding on purpose. A joining player receives
// hundreds of columns and reads blocks out of very few of them, so Split47 and
// Split775 walk a column into per-section byte ranges cheaply, and the caller
// decodes the sections it is asked about. The ranges alias the column the
// caller passed in, which stays the caller's to keep and must not be written
// to afterwards.
package chunk

import (
	"errors"
	"fmt"
	"io"

	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// BlocksPerSection is the block count of one 16x16x16 section, in every
// protocol this package reads.
const BlocksPerSection = 16 * 16 * 16

var (
	// ErrColumn reports a column blob that does not match the layout its
	// protocol describes.
	ErrColumn = errors.New("chunk column is not decodable")
	// ErrSection reports a section whose bytes do not decode to blocks.
	ErrSection = errors.New("chunk section is not decodable")
)

// Section is one 16x16x16 slice of a column as it arrived.
//
// Blocks and Biomes alias the column they were split out of. They are the
// bytes to hand back to this package's decode functions; nothing else in a
// Section is needed to read a block out of it.
//
// A field only one protocol carries is zero in the other. Protocol 47 has no
// per-section biomes — a column carries 256 of them, in Column47.Biomes — and
// declares no counts. Protocol 775 carries both.
type Section struct {
	// Y is the section's index in the world, not its index in the blob. It is
	// the block Y divided by sixteen, so it is negative below y=0.
	Y int
	// Blocks is the section's block-state container, header included.
	Blocks []byte
	// Biomes is the section's biome container, header included. Protocol 775
	// only.
	Biomes []byte
	// BlockCount is the number of blocks the server counted as non-empty, and
	// FluidCount the number it counted as fluid. Protocol 775 only.
	//
	// Neither is read by any decode here: what counts as empty is a block
	// semantic, and this package owns wire layout rather than block meaning.
	// They are kept because a server that packed the bytes stating what it put
	// in them is an independent check on a decoder that reads them back.
	BlockCount int16
	FluidCount int16
}

// reader walks a column's bytes.
//
// It is an io.Reader so that VarInts are read by this module's own VarInt
// reader rather than by a second copy of that arithmetic, and a cursor so that
// what it hands back aliases the column instead of copying it.
type reader struct {
	data []byte
	pos  int
}

// Read implements io.Reader.
func (r *reader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n

	return n, nil
}

func (r *reader) byteValue() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("%w: a byte past the end", ErrColumn)
	}
	value := r.data[r.pos]
	r.pos++

	return value, nil
}

func (r *reader) short() (int16, error) {
	if r.pos+2 > len(r.data) {
		return 0, fmt.Errorf("%w: a short past the end", ErrColumn)
	}
	value := int16(r.data[r.pos])<<8 | int16(r.data[r.pos+1])
	r.pos += 2

	return value, nil
}

func (r *reader) varint() (int32, error) {
	value, _, err := java.ReadVarInt(r)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrColumn, err)
	}

	return value, nil
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.data) {
		return nil, fmt.Errorf("%w: %d bytes past the end", ErrColumn, n)
	}
	taken := r.data[r.pos : r.pos+n]
	r.pos += n

	return taken, nil
}
