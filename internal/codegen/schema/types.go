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

// UnmarshalJSON accepts both forms a block drop takes upstream.
//
// Java 1.8 wraps every drop in an object carrying optional counts, so the
// entry is {"drop": ...}. Java 26.1 dropped the wrapper and lists bare item
// IDs. Decoding is strict, so a version's own shape has to be understood here
// rather than tolerated by ignoring what does not fit.
func (d *RawDrop) UnmarshalJSON(raw []byte) error {
	var bare json.Number
	if err := json.Unmarshal(raw, &bare); err == nil {
		d.Drop = append([]byte(nil), raw...)
		d.MinCount, d.MaxCount = nil, nil

		return nil
	}

	// The alias avoids recursing into this method.
	type wrapped RawDrop
	var object wrapped
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&object); err != nil {
		return fmt.Errorf("drop must be an item ID or an object: %w", err)
	}
	*d = RawDrop(object)

	return nil
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

// Sound describes one source sound-event record.
type Sound struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// MapIcon describes one source filled-map marker record.
type MapIcon struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Appearance         string `json:"appearance"`
	VisibleInItemFrame bool   `json:"visibleInItemFrame"`
}

// LootDrop describes one drop in a source loot table. StackSizeRange holds
// nullable bounds because upstream publishes a two-element array whose members
// it does not promise to fill.
type LootDrop struct {
	Item           string  `json:"item"`
	DropChance     float64 `json:"dropChance"`
	StackSizeRange []*int  `json:"stackSizeRange"`
	SilkTouch      bool    `json:"silkTouch"`
	NoSilkTouch    bool    `json:"noSilkTouch"`
	BlockAge       *int    `json:"blockAge"`
	PlayerKill     bool    `json:"playerKill"`
}

// BlockLoot describes one source block loot table.
type BlockLoot struct {
	Block string     `json:"block"`
	Drops []LootDrop `json:"drops"`
}

// EntityLoot describes one source entity loot table.
type EntityLoot struct {
	Entity string     `json:"entity"`
	Drops  []LootDrop `json:"drops"`
}

// CommandTree describes the source command dataset: the tree a server
// publishes and the catalogue of parsers its argument nodes draw on.
type CommandTree struct {
	Root    CommandNode     `json:"root"`
	Parsers []CommandParser `json:"parsers"`
}

// CommandNode describes one source command tree node.
type CommandNode struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	Executable bool           `json:"executable"`
	Redirects  []string       `json:"redirects"`
	Children   []CommandNode  `json:"children"`
	Parser     *CommandParser `json:"parser"`
}

// CommandParser describes a source Brigadier parser. Examples appear in the
// catalogue only; a tree node publishes the parser and its modifier alone.
type CommandParser struct {
	Parser   string           `json:"parser"`
	Modifier *CommandModifier `json:"modifier"`
	Examples []string         `json:"examples"`
}

// CommandModifier describes the properties that configure a source parser.
type CommandModifier struct {
	Type     string   `json:"type"`
	Amount   string   `json:"amount"`
	Registry string   `json:"registry"`
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
}

// LoginPacket describes the source sample play-login packet.
type LoginPacket struct {
	EntityID            int32           `json:"entityId"`
	IsHardcore          bool            `json:"isHardcore"`
	WorldNames          []string        `json:"worldNames"`
	MaxPlayers          int             `json:"maxPlayers"`
	ViewDistance        int             `json:"viewDistance"`
	SimulationDistance  int             `json:"simulationDistance"`
	ReducedDebugInfo    bool            `json:"reducedDebugInfo"`
	EnableRespawnScreen bool            `json:"enableRespawnScreen"`
	DoLimitedCrafting   bool            `json:"doLimitedCrafting"`
	WorldState          LoginWorldState `json:"worldState"`
	EnforcesSecureChat  bool            `json:"enforcesSecureChat"`
	DimensionCodec      json.RawMessage `json:"dimensionCodec"`
}

// LoginWorldState describes the world in the source sample login packet.
type LoginWorldState struct {
	Dimension        int     `json:"dimension"`
	Name             string  `json:"name"`
	HashedSeed       []int32 `json:"hashedSeed"`
	Gamemode         string  `json:"gamemode"`
	PreviousGamemode int     `json:"previousGamemode"`
	IsDebug          bool    `json:"isDebug"`
	IsFlat           bool    `json:"isFlat"`
	PortalCooldown   int     `json:"portalCooldown"`
	SeaLevel         int     `json:"seaLevel"`
}

// TintCategory describes one source tint category.
type TintCategory struct {
	Data []TintEntry `json:"data"`
}

// TintEntry describes one colour and the keys that take it. The keys are raw
// because a category keys by biome name and redstone keys by power level.
type TintEntry struct {
	Keys  []json.RawMessage `json:"keys"`
	Color int               `json:"color"`
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

// LoadJSONValue decodes a source JSON document that is not an array. It is
// strict for the same reason LoadJSON is: a field nothing models is an error
// naming it, rather than a value that silently disappears.
func LoadJSONValue[T any](raw []byte) (T, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("unmarshal: %w", err)
	}

	return value, nil
}
