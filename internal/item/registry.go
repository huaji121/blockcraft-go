package item

import (
	"errors"
	"fmt"
	"sync"

	"blockcraft-go/internal/world"
)

// Registry is the store of registered items, indexed by item ID and by the
// block an item places. Register happens at startup; reads happen at runtime
// from the inventory/hotbar. The mutex keeps concurrent reads safe if item
// lookups ever move off the main thread.
type Registry struct {
	mu      sync.RWMutex
	byID    map[string]*Item
	byBlock map[world.BlockID]*Item
}

// NewRegistry returns an empty item registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:    make(map[string]*Item),
		byBlock: make(map[world.BlockID]*Item),
	}
}

// Register adds an item to the registry. It applies the default stack size,
// rejects duplicate IDs and duplicate block references, and is safe for
// concurrent registration. Returns an error if the item is invalid or a
// conflict exists.
func (r *Registry) Register(item *Item) error {
	if item == nil {
		return errors.New("cannot register nil item")
	}
	if item.ID == "" {
		return errors.New("item ID is empty")
	}
	if item.StackSize <= 0 {
		item.StackSize = DefaultStackSize
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[item.ID]; exists {
		return fmt.Errorf("item %q already registered", item.ID)
	}
	if item.BlockReference != nil {
		if _, exists := r.byBlock[*item.BlockReference]; exists {
			return fmt.Errorf("block %d already has a registered item", *item.BlockReference)
		}
	}

	r.byID[item.ID] = item
	if item.BlockReference != nil {
		r.byBlock[*item.BlockReference] = item
	}
	return nil
}

// Get returns the item with the given ID, or an error if none is registered.
func (r *Registry) Get(id string) (*Item, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("item %q not registered", id)
	}
	return item, nil
}

// GetByBlock returns the item that places the given block, or an error if no
// such item is registered.
func (r *Registry) GetByBlock(block world.BlockID) (*Item, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.byBlock[block]
	if !ok {
		return nil, fmt.Errorf("no item registered for block %d", block)
	}
	return item, nil
}

// ListAll returns every registered item. The order is not guaranteed.
func (r *Registry) ListAll() []*Item {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Item, 0, len(r.byID))
	for _, item := range r.byID {
		out = append(out, item)
	}
	return out
}

// RegisterBlockItems registers a block item for every non-air block type, with
// ID "item_<block name>". It is the default seeding of the registry and is
// idempotent only if the registry is empty; calling it twice on the same
// registry returns an error on the first duplicate.
func RegisterBlockItems(r *Registry) error {
	for b := world.BlockID(1); b < world.NumBlockTypes; b++ {
		if err := r.Register(NewBlockItem(b)); err != nil {
			return err
		}
	}
	return nil
}
