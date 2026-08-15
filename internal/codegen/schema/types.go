// Package schema defines the source JSON documents consumed by code generation.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Block describes one source block record.
type Block struct {
	ID           int             `json:"id"`
	Name         string          `json:"name"`
	DisplayName  string          `json:"displayName"`
	Hardness     *float64        `json:"hardness"`
	StackSize    int             `json:"stackSize"`
	Diggable     bool            `json:"diggable"`
	BoundingBox  string          `json:"boundingBox"`
	Material     string          `json:"material"`
	Transparent  bool            `json:"transparent"`
	EmitLight    int             `json:"emitLight"`
	FilterLight  int             `json:"filterLight"`
	Resistance   float64         `json:"resistance"`
	Drops        []RawDrop       `json:"drops"`
	HarvestTools map[string]bool `json:"harvestTools"`
	// Variations is Java 1.8 only: metadata values were how a block carried
	// its variants before block states existed.
	Variations []RawVariation `json:"variations"`
	// DefaultState, MinStateID, MaxStateID, and States replaced metadata
	// variants in the flattening. They are absent from Java 1.8.
	DefaultState int          `json:"defaultState"`
	MinStateID   int          `json:"minStateId"`
	MaxStateID   int          `json:"maxStateId"`
	States       []BlockState `json:"states"`
}

// BlockState describes one property a modern block state varies over.
type BlockState struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	NumValues int      `json:"num_values"`
	Values    []string `json:"values"`
}

// RawDrop retains the integer-or-object drop representation from source JSON.
type RawDrop struct {
	Drop     json.RawMessage `json:"drop"`
	MinCount *float64        `json:"minCount"`
	MaxCount *float64        `json:"maxCount"`
}

// DropObject is the object form of a block drop.
type DropObject struct {
	ID       int `json:"id"`
	Metadata int `json:"metadata"`
}

// Parse converts a raw drop to its scalar fields without applying defaults.
func (d *RawDrop) Parse() (id, metadata int, err error) {
	var plainID int
	if err := json.Unmarshal(d.Drop, &plainID); err == nil {
		return plainID, 0, nil
	}

	var object DropObject
	if err := json.Unmarshal(d.Drop, &object); err == nil {
		return object.ID, object.Metadata, nil
	}

	return 0, 0, fmt.Errorf("drop must be an item ID or object")
}

// RawVariation describes a source metadata variation.
type RawVariation struct {
	Metadata    int    `json:"metadata"`
	DisplayName string `json:"displayName"`
}

// Item describes one source item record.
type Item struct {
	ID                int            `json:"id"`
	Name              string         `json:"name"`
	DisplayName       string         `json:"displayName"`
	StackSize         int            `json:"stackSize"`
	MaxDurability     int            `json:"maxDurability"`
	EnchantCategories []string       `json:"enchantCategories"`
	RepairWith        []string       `json:"repairWith"`
	Variations        []RawVariation `json:"variations"`
}

// Entity describes one source entity record.
type Entity struct {
	ID          int      `json:"id"`
	InternalID  int      `json:"internalId"`
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Type        string   `json:"type"`
	Width       *float64 `json:"width"`
	Height      *float64 `json:"height"`
	Category    string   `json:"category"`
	// MetadataKeys names the entity's metadata fields in wire order. Java 1.8
	// does not publish it.
	MetadataKeys []string `json:"metadataKeys"`
}

// Biome describes one source biome record.
type Biome struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	NameLegacy    string  `json:"name_legacy"`
	DisplayName   string  `json:"displayName"`
	Category      string  `json:"category"`
	Temperature   float64 `json:"temperature"`
	Precipitation string  `json:"precipitation"`
	Depth         float64 `json:"depth"`
	Dimension     string  `json:"dimension"`
	Color         int     `json:"color"`
	Rainfall      float64 `json:"rainfall"`
	// Climates is Java 1.8 only and is null in every record upstream ships.
	Climates json.RawMessage `json:"climates"`
	// HasPrecipitation replaced the 1.8 precipitation string.
	HasPrecipitation bool `json:"has_precipitation"`
}

// Effect describes one source effect record.
type Effect struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type"`
}

// Enchantment describes one source enchantment record.
type Enchantment struct {
	ID           int         `json:"id"`
	Name         string      `json:"name"`
	DisplayName  string      `json:"displayName"`
	MaxLevel     int         `json:"maxLevel"`
	MinCost      EnchantCost `json:"minCost"`
	MaxCost      EnchantCost `json:"maxCost"`
	Exclude      []string    `json:"exclude"`
	Category     string      `json:"category"`
	Weight       int         `json:"weight"`
	TreasureOnly bool        `json:"treasureOnly"`
	Curse        bool        `json:"curse"`
	Tradeable    bool        `json:"tradeable"`
	Discoverable bool        `json:"discoverable"`
}

// EnchantCost describes a source enchantment cost formula.
type EnchantCost struct {
	A int `json:"a"`
	B int `json:"b"`
}

// Food describes one source food record.
type Food struct {
	ID               int            `json:"id"`
	Name             string         `json:"name"`
	DisplayName      string         `json:"displayName"`
	StackSize        int            `json:"stackSize"`
	FoodPoints       float64        `json:"foodPoints"`
	Saturation       float64        `json:"saturation"`
	EffectiveQuality float64        `json:"effectiveQuality"`
	SaturationRatio  float64        `json:"saturationRatio"`
	Variations       []RawVariation `json:"variations"`
}

// Particle describes one source particle record.
type Particle struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Instrument describes one source instrument record.
type Instrument struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Attribute describes one source entity attribute record.
type Attribute struct {
	Name     string  `json:"name"`
	Resource string  `json:"resource"`
	Default  float64 `json:"default"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
}

// Window describes one source inventory window record.
type Window struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Slots      []WindowSlot   `json:"slots"`
	Properties []string       `json:"properties"`
	OpenedWith []WindowOpener `json:"openedWith"`
}

// WindowSlot describes a source window slot range.
type WindowSlot struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	Size  int    `json:"size"`
}

// WindowOpener describes a source object that opens a window.
type WindowOpener struct {
	Type string `json:"type"`
	ID   int    `json:"id"`
}

// VersionInfo describes the version metadata in version.json.
type VersionInfo struct {
	Version          int    `json:"version"`
	MinecraftVersion string `json:"minecraftVersion"`
	MajorVersion     string `json:"majorVersion"`
}

// RawRecipe retains source ingredients before union parsing.
type RawRecipe struct {
	Ingredients []json.RawMessage   `json:"ingredients"`
	InShape     [][]json.RawMessage `json:"inShape"`
	OutShape    [][]json.RawMessage `json:"outShape"`
	Result      RecipeResult        `json:"result"`
}

// RecipeResult describes a source recipe result.
type RecipeResult struct {
	ID       int `json:"id"`
	Count    int `json:"count"`
	Metadata int `json:"metadata"`
}

// RecipeIngredient is the normalized source ingredient representation.
type RecipeIngredient struct {
	ID       int
	Metadata int
}

// ParseIngredient normalizes integer and object ingredient representations.
func ParseIngredient(raw json.RawMessage) RecipeIngredient {
	var plainID int
	if err := json.Unmarshal(raw, &plainID); err == nil {
		return RecipeIngredient{ID: plainID, Metadata: -1}
	}

	var object struct {
		ID       int  `json:"id"`
		Metadata *int `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		metadata := -1
		if object.Metadata != nil {
			metadata = *object.Metadata
		}
		return RecipeIngredient{ID: object.ID, Metadata: metadata}
	}

	return RecipeIngredient{}
}

// RawCollisionShapes retains the scalar-or-array source shape indexes.
type RawCollisionShapes struct {
	Blocks map[string]json.RawMessage `json:"blocks"`
	Shapes map[string][][]float64     `json:"shapes"`
}

// LoadJSON decodes a source JSON array.
func LoadJSON[T any](raw []byte) ([]T, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var items []T
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return items, nil
}
