package data

import "maps"

// BlockMovementRegistry answers whether a block stops something walking into
// it.
//
// It is separate from BlockRegistry because the fact has a different source.
// Upstream's block data says what a block is called, how hard it is, and what
// it drops; it does not say whether an entity can occupy its cell. That answer
// lives in the game's own material, so it is measured out of a Mojang jar and
// arrives here as an extracted dataset, the same way physics constants do. A
// version nobody has measured publishes no registry at all rather than an empty
// one, because "no measurement" and "nothing blocks movement" are not the same
// statement.
type BlockMovementRegistry interface {
	// ByState reports whether the block state a chunk carries stops movement.
	// The second result is false when the measurement does not describe the
	// state.
	//
	// Unknown is not "passable". A caller that reads a missing answer as "the
	// bot may walk here" walks into walls the measurement simply did not
	// mention; refusing the position is the only safe reading.
	ByState(BlockStateID) (bool, bool)
	// ByID reports the same fact for a block identifier, for a caller that
	// already resolved one through BlockRegistry.
	ByID(BlockID) (bool, bool)
	// All returns the measurement keyed by block identifier. The caller owns
	// the returned map.
	All() BlockMovementIndex
}

// BlockMovementIndex maps block identifiers to whether they stop movement.
type BlockMovementIndex map[BlockID]bool

// Clone returns an index that does not alias the source.
func (b BlockMovementIndex) Clone() BlockMovementIndex { return maps.Clone(b) }
