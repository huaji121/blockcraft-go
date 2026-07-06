package world

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"blockcraft-go/internal/math3d"
)

// ChunkSize is the edge length of a chunk in blocks. Chunks are cubes of
// ChunkSize^3 cells. 32 is chosen to match the task spec and keeps a single
// chunk's block array at 32 KiB.
const ChunkSize = 32

// ChunkSize2 is the number of blocks in a chunk layer (ChunkSize*ChunkSize).
const ChunkSize2 = ChunkSize * ChunkSize

// ChunkSize3 is the number of blocks in a whole chunk.
const ChunkSize3 = ChunkSize * ChunkSize * ChunkSize

// ChunkPos is the discrete coordinate of a chunk in chunk-space. The world
// position of the chunk's minimum corner is (Pos * ChunkSize).
type ChunkPos struct {
	X, Y, Z int32
}

// Key returns a comparable key usable as a map key.
func (p ChunkPos) Key() [3]int32 { return [3]int32{p.X, p.Y, p.Z} }

// WorldOrigin returns the world-space block coordinate of the chunk's min corner.
func (p ChunkPos) WorldOrigin() (int32, int32, int32) {
	return p.X * ChunkSize, p.Y * ChunkSize, p.Z * ChunkSize
}

// Chunk holds the voxel data for one ChunkSize³ region of the world.
type Chunk struct {
	Pos    ChunkPos
	Blocks [ChunkSize3]BlockID
	// dirty indicates that the chunk's mesh is out of date and must be
	// regenerated before it is rendered.
	dirty bool
	// empty is set when generation produced no solid blocks, letting the
	// renderer skip allocating GPU buffers for it.
	empty bool
}

// NewChunk allocates an empty chunk at the given position.
func NewChunk(pos ChunkPos) *Chunk {
	c := &Chunk{Pos: pos, dirty: true}
	return c
}

// index returns the flat array index for local block coordinates.
func index(lx, ly, lz int) int {
	return ly*ChunkSize2 + lz*ChunkSize + lx
}

// inBounds reports whether local coordinates are inside the chunk.
func inBounds(lx, ly, lz int) bool {
	return lx >= 0 && lx < ChunkSize && ly >= 0 && ly < ChunkSize && lz >= 0 && lz < ChunkSize
}

// GetLocal returns the block at local coordinates. Out-of-range coordinates are
// treated as air; cross-chunk lookups must go through the World.
func (c *Chunk) GetLocal(lx, ly, lz int) BlockID {
	if !inBounds(lx, ly, lz) {
		return Air
	}
	return c.Blocks[index(lx, ly, lz)]
}

// SetLocal sets the block at local coordinates and marks the chunk (and any
// chunk sharing an edited border face) as needing a remesh.
func (c *Chunk) SetLocal(lx, ly, lz int, b BlockID) {
	if !inBounds(lx, ly, lz) {
		return
	}
	c.Blocks[index(lx, ly, lz)] = b
	c.dirty = true
}

// IsDirty reports whether the chunk needs remeshing.
func (c *Chunk) IsDirty() bool { return c.dirty }

// MarkClean clears the dirty flag after a mesh has been built for the chunk.
func (c *Chunk) MarkClean() { c.dirty = false }

// IsEmpty reports whether the chunk contains no solid blocks.
func (c *Chunk) IsEmpty() bool { return c.empty }

// SetEmpty records whether generation produced a solid-free chunk.
func (c *Chunk) SetEmpty(v bool) { c.empty = v }

// AABB returns the world-space axis-aligned bounding box of the chunk's blocks.
// The box spans [origin, origin+ChunkSize] on each axis.
func (c *Chunk) AABB() math3d.AABB {
	ox, oy, oz := c.Pos.WorldOrigin()
	return math3d.AABB{
		Min: mgl32.Vec3{float32(ox), float32(oy), float32(oz)},
		Max: mgl32.Vec3{float32(ox + ChunkSize), float32(oy + ChunkSize), float32(oz + ChunkSize)},
	}
}

// String helps with debug logging.
func (c *Chunk) String() string {
	return fmt.Sprintf("Chunk(%d,%d,%d)", c.Pos.X, c.Pos.Y, c.Pos.Z)
}
