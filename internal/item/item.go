// Package item defines the item data model and registry.
//
// An Item is anything the player can hold in an inventory slot. Block items
// reference a world.BlockID so the inventory can place/render them; non-block
// items (tools, future) leave BlockReference nil and carry their own texture.
package item

import (
	"strings"

	"blockcraft-go/internal/world"
)

// DefaultStackSize is the maximum stack size for items that don't specify one.
const DefaultStackSize = 64

// Item describes a holdable item.
type Item struct {
	// ID is the unique identifier, conventionally "item_<block name>" for
	// block items (e.g. "item_dirt").
	ID string
	// Name is the human-readable display name shown in the inventory UI.
	Name string
	// StackSize is the maximum number of this item per inventory slot.
	// Defaults to DefaultStackSize (64) when zero.
	StackSize int
	// BlockReference points to the block this item places, or nil for
	// non-block items. It is a pointer so non-block items can omit it.
	BlockReference *world.BlockID
	// TexturePath is the texture name used to render the item in the
	// inventory. For block items this is the block's side texture name
	// (resolvable through the atlas); for non-block items it is an explicit
	// texture name.
	TexturePath string
	// IsTool reserves this item as a tool. Tool behaviour is not yet
	// implemented; the flag lets the inventory distinguish tools early.
	IsTool bool
}

// NewBlockItem builds the canonical item for a block type: ID "item_<name>",
// display name title-cased, default stack size, and the block's side texture.
func NewBlockItem(block world.BlockID) *Item {
	b := block
	name := block.Name()
	return &Item{
		ID:             "item_" + name,
		Name:           displayName(name),
		StackSize:      DefaultStackSize,
		BlockReference: &b,
		TexturePath:    world.BlockSideTextureName(block),
		IsTool:         false,
	}
}

// displayName converts a snake_case block name into a Title Case display name,
// e.g. "oak_log" -> "Oak Log", "dirt" -> "Dirt".
func displayName(blockName string) string {
	words := strings.Split(blockName, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
