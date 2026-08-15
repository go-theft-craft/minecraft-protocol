package data

import "slices"

// EntityID identifies a Minecraft entity.
type EntityID int

// EntityInternalID identifies a Minecraft entity in the protocol internals.
type EntityInternalID int

// EntityType identifies the source namespace for an entity ID.
type EntityType string

const (
	// EntityTypeMob identifies entities from the mob ID namespace.
	EntityTypeMob EntityType = "mob"
	// EntityTypeObject identifies entities from the object ID namespace.
	EntityTypeObject EntityType = "object"

	// EntityTypeAmbient classifies an ambient creature.
	//
	// This and the classifications below appear from Java 26.1 onward, where
	// the two ID namespaces of 1.8 gave way to a classification of the entity
	// itself. They are listed rather than accepted as free text, so a
	// classification nobody has seen still fails generation.
	EntityTypeAmbient EntityType = "ambient"
	// EntityTypeAnimal classifies a farm or wild animal.
	EntityTypeAnimal EntityType = "animal"
	// EntityTypeHostile classifies an entity that attacks unprovoked.
	EntityTypeHostile EntityType = "hostile"
	// EntityTypeLiving classifies a living entity with no narrower category.
	EntityTypeLiving EntityType = "living"
	// EntityTypeOther classifies an entity that is none of the rest.
	EntityTypeOther EntityType = "other"
	// EntityTypePassive classifies an entity that does not attack.
	EntityTypePassive EntityType = "passive"
	// EntityTypePlayer classifies a player entity.
	EntityTypePlayer EntityType = "player"
	// EntityTypeProjectile classifies a thrown or fired entity.
	EntityTypeProjectile EntityType = "projectile"
	// EntityTypeWaterCreature classifies an aquatic creature.
	EntityTypeWaterCreature EntityType = "water_creature"
)

// Entity describes a Minecraft entity.
type Entity struct {
	ID          EntityID
	InternalID  EntityInternalID
	Name        string
	DisplayName string
	Type        EntityType
	Width       *float64
	Height      *float64
	Category    string
	// MetadataKeys names the entity's metadata fields in wire order, so an
	// index in an entity-metadata packet can be read as a name. Java 1.8 does
	// not publish it and leaves this empty.
	MetadataKeys []string
}

// Entities is a collection of Minecraft entities.
type Entities []Entity

// Clone returns entities whose mutable fields do not alias the source.
func (e Entities) Clone() Entities {
	if e == nil {
		return nil
	}

	clone := make(Entities, len(e))
	for index := range clone {
		clone[index] = e[index].Clone()
	}

	return clone
}

// Clone returns an Entity whose mutable fields do not alias the source.
func (e Entity) Clone() Entity {
	clone := e
	if e.Width != nil {
		width := *e.Width
		clone.Width = &width
	}
	if e.Height != nil {
		height := *e.Height
		clone.Height = &height
	}
	clone.MetadataKeys = slices.Clone(e.MetadataKeys)

	return clone
}
