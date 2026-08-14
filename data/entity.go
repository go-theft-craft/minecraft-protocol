package data

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

	return clone
}
