package data

import (
	"maps"
	"slices"
)

// ShapeID identifies a collision shape.
type ShapeID int

// CollisionShapes describes collision geometry for blocks.
type CollisionShapes struct {
	Blocks BlockShapeIndex
	Shapes BoundingBoxIndex
}

// BoundingBox describes a rectangular collision volume.
type BoundingBox struct {
	MinX, MinY, MinZ float64
	MaxX, MaxY, MaxZ float64
}

// ShapeIDs is a collection of collision-shape IDs.
type ShapeIDs []ShapeID

// Clone returns shape IDs that do not alias the source.
func (s ShapeIDs) Clone() ShapeIDs { return slices.Clone(s) }

// BoundingBoxes is a collection of collision bounding boxes.
type BoundingBoxes []BoundingBox

// Clone returns bounding boxes that do not alias the source.
func (b BoundingBoxes) Clone() BoundingBoxes { return slices.Clone(b) }

// BlockShapeIndex maps block names to collision-shape IDs.
type BlockShapeIndex map[string]ShapeIDs

// Clone returns an index whose shape-ID slices do not alias the source.
func (b BlockShapeIndex) Clone() BlockShapeIndex {
	clone := maps.Clone(b)
	for block, shapeIDs := range clone {
		clone[block] = shapeIDs.Clone()
	}

	return clone
}

// BoundingBoxIndex maps collision-shape IDs to bounding boxes.
type BoundingBoxIndex map[ShapeID]BoundingBoxes

// Clone returns an index whose bounding-box slices do not alias the source.
func (b BoundingBoxIndex) Clone() BoundingBoxIndex {
	clone := maps.Clone(b)
	for shapeID, boxes := range clone {
		clone[shapeID] = boxes.Clone()
	}

	return clone
}

// Clone returns CollisionShapes whose maps and map values do not alias the source.
func (c CollisionShapes) Clone() CollisionShapes {
	clone := c
	clone.Blocks = c.Blocks.Clone()
	clone.Shapes = c.Shapes.Clone()

	return clone
}
