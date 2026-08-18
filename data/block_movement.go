package data

import "maps"

// BlockMovementRegistry answers what a block does to a body trying to move
// through, over, or under it: whether it stops something walking into it,
// whether it falls when undermined, and whether it can be climbed.
//
// It is separate from BlockRegistry because the fact has a different source.
// Upstream's block data says what a block is called, how hard it is, and what
// it drops; it does not say whether an entity can occupy its cell. That answer
// lives in the game's own material, so it is measured out of a Mojang jar and
// arrives here as an extracted dataset, the same way physics constants do. A
// version nobody has measured publishes no registry at all rather than an empty
// one, because "no measurement" and "nothing blocks movement" are not the same
// statement.
//
// Falling and climbing arrive from the same extraction pass and are recorded
// the same way, because upstream publishes neither. They differ from the
// movement fact in one respect worth knowing: both hang off the block in every
// version measured so far, so unlike ByID, FallsByID and ClimbableByID never
// have to decline because a block's states disagree.
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
	// FallsByState reports whether the block a state belongs to is pulled down
	// when the block beneath it is removed. The second result is false when the
	// measurement does not describe the state.
	//
	// A route that digs has to know this before it digs, and nothing else
	// answers it. Material will not substitute: soul sand shares Material.sand
	// with gravel and stays where it is, which is why this is measured out of
	// the game rather than derived from something already published.
	//
	// Unknown is not "stays put", for the same reason unknown is not
	// "passable". A caller that reads a missing answer as a negative digs out
	// the bottom of a gravel column the measurement never mentioned.
	FallsByState(BlockStateID) (bool, bool)
	// FallsByID reports the same fact for a block identifier.
	FallsByID(BlockID) (bool, bool)
	// ClimbableByState reports whether a body can climb the column this state
	// occupies — a ladder, a vine, and in later versions several more. The
	// second result is false when the measurement does not describe the state.
	//
	// It is here rather than derived from a shape because a shape cannot say
	// it. A ladder's collision box is empty, so a caller reading collision
	// alone cannot tell one from air, and the two lead somewhere very
	// different.
	ClimbableByState(BlockStateID) (bool, bool)
	// ClimbableByID reports the same fact for a block identifier.
	ClimbableByID(BlockID) (bool, bool)
	// All returns the measurement keyed by block identifier. The caller owns
	// the returned map.
	All() BlockMovementIndex
}

// BlockMovementIndex maps block identifiers to whether they stop movement.
type BlockMovementIndex map[BlockID]bool

// Clone returns an index that does not alias the source.
func (b BlockMovementIndex) Clone() BlockMovementIndex { return maps.Clone(b) }
