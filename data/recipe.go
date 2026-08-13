package data

import (
	"maps"
	"slices"
)

// Recipe describes a Minecraft crafting recipe.
type Recipe struct {
	Ingredients RecipeIngredients
	InShape     RecipeShape
	Result      RecipeResult
}

// Ingredient describes an item used by a recipe.
type Ingredient struct {
	ID       ItemID
	Metadata Metadata
}

// RecipeResult describes an item produced by a recipe.
type RecipeResult struct {
	ID       ItemID
	Count    int
	Metadata Metadata
}

// RecipeIngredients is a collection of ingredients in a recipe.
type RecipeIngredients []Ingredient

// Clone returns ingredients that do not alias the source.
func (r RecipeIngredients) Clone() RecipeIngredients {
	return slices.Clone(r)
}

// RecipeShape is a collection of ingredient rows in a shaped recipe.
type RecipeShape []RecipeIngredients

// Clone returns recipe rows whose ingredients do not alias the source.
func (r RecipeShape) Clone() RecipeShape {
	clone := slices.Clone(r)
	for index := range clone {
		clone[index] = r[index].Clone()
	}

	return clone
}

// Recipes is a collection of crafting recipes.
type Recipes []Recipe

// Clone returns recipes whose mutable fields do not alias the source.
func (r Recipes) Clone() Recipes {
	if r == nil {
		return nil
	}

	clone := make(Recipes, len(r))
	for index := range clone {
		clone[index] = r[index].Clone()
	}

	return clone
}

// RecipeIndex indexes recipe collections by result item ID.
type RecipeIndex map[ItemID]Recipes

// Clone returns an index whose recipes do not alias the source.
func (r RecipeIndex) Clone() RecipeIndex {
	clone := maps.Clone(r)
	for id, recipes := range clone {
		clone[id] = recipes.Clone()
	}

	return clone
}

// Clone returns a Recipe whose mutable fields do not alias the source.
func (r Recipe) Clone() Recipe {
	clone := r
	clone.Ingredients = r.Ingredients.Clone()
	clone.InShape = r.InShape.Clone()

	return clone
}
