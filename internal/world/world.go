package world

import (
	"math"
	"sort"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
)

// MeshUpload is a chunk mesh produced on the main thread that the renderer
// uploads to GPU buffers. An empty Data (zero vertices) means the chunk's
// previous mesh should be freed and replaced with nothing.
type MeshUpload struct {
	Pos  ChunkPos
	Data ChunkMeshData
}

// World is the central chunk store and manager. It owns:
//   - the in-memory chunk map (accessed only from the main thread),
//   - a pool of background goroutines that generate chunk block data,
//   - a throttled on-main-thread mesher that turns chunks into render data.
//
// Threading model: only the main thread touches the chunks map. Background
// workers generate standalone Chunk objects and hand them back through a
// channel, so no locking is needed on the map itself. This keeps the hot path
// lock-free while still offloading the expensive terrain generation.
type World struct {
	seed       int64
	renderDist int
	atlas      AtlasUVProvider

	chunks map[[3]int32]*Chunk
	queued map[[3]int32]bool // chunks enqueued for generation, not yet returned

	genQueue  chan ChunkPos
	generated chan *Chunk
	stopCh    chan struct{}
	workers   sync.WaitGroup

	playerChunk ChunkPos

	pendingUploads []MeshUpload
	pendingUnloads []ChunkPos

	// meshPerFrame caps the number of chunks meshed per frame so frame time
	// stays bounded while a fresh area loads.
	meshPerFrame int
}

// NewWorld creates a world with the given seed and XZ render distance (in
// chunks). The atlas is used by the mesher to resolve tile UVs.
func NewWorld(seed int64, renderDist int, atlas AtlasUVProvider) *World {
	w := &World{
		seed:         seed,
		renderDist:   renderDist,
		atlas:        atlas,
		chunks:       make(map[[3]int32]*Chunk),
		queued:       make(map[[3]int32]bool),
		genQueue:     make(chan ChunkPos, 64),
		generated:    make(chan *Chunk, 64),
		stopCh:       make(chan struct{}),
		meshPerFrame: 2,
	}
	return w
}

// Start launches the background terrain-generation workers.
func (w *World) Start(numWorkers int) {
	if numWorkers < 1 {
		numWorkers = 1
	}
	for range numWorkers {
		w.workers.Add(1)
		go w.worker()
	}
}

// Close stops the workers and waits for them to exit.
func (w *World) Close() {
	close(w.stopCh)
	w.workers.Wait()
}

// worker generates chunk block data off the main thread.
func (w *World) worker() {
	defer w.workers.Done()
	for {
		select {
		case <-w.stopCh:
			return
		case pos := <-w.genQueue:
			c := NewChunk(pos)
			generateTerrain(c, w.seed)
			// Hand the finished chunk back; if Close happened meanwhile the
			// send would block, so guard with a select on stopCh.
			select {
			case w.generated <- c:
			case <-w.stopCh:
				return
			}
		}
	}
}

// Update advances the world by one frame: it absorbs newly generated chunks,
// requests missing chunks around the player, unloads distant ones and meshes a
// throttled batch of dirty chunks. Must be called from the main thread.
func (w *World) Update(playerPos mgl32.Vec3) {
	pcx, pcy, pcz := worldToChunk(int32(math.Floor(float64(playerPos.X()))),
		int32(math.Floor(float64(playerPos.Y()))),
		int32(math.Floor(float64(playerPos.Z()))))
	w.playerChunk = ChunkPos{pcx, pcy, pcz}

	w.drainGenerated()
	w.enqueueNeeded()
	w.unloadDistant()
	w.meshDirty()
}

// Prefill synchronously generates and meshes the chunks immediately around the
// given position so the player has solid ground and something to see on the
// very first frame, before the async loader has caught up.
func (w *World) Prefill(playerPos mgl32.Vec3) {
	pcx, _, pcz := worldToChunk(int32(math.Floor(float64(playerPos.X()))),
		int32(math.Floor(float64(playerPos.Y()))),
		int32(math.Floor(float64(playerPos.Z()))))
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			for cy := MinChunkY; cy <= MaxChunkY; cy++ {
				pos := ChunkPos{pcx + dx, cy, pcz + dz}
				if _, ok := w.chunks[pos.Key()]; ok {
					continue
				}
				c := NewChunk(pos)
				generateTerrain(c, w.seed)
				w.chunks[pos.Key()] = c
			}
		}
	}
	w.playerChunk = ChunkPos{pcx, 0, pcz}
	// Mesh every ready chunk immediately.
	prev := w.meshPerFrame
	w.meshPerFrame = 1 << 30
	w.meshDirty()
	w.meshPerFrame = prev
}

// drainGenerated absorbs finished chunks from the workers into the map.
func (w *World) drainGenerated() {
	for {
		select {
		case c := <-w.generated:
			delete(w.queued, c.Pos.Key())
			// Discard if the chunk scrolled out of range while being generated.
			if !w.inRangeXZ(c.Pos.X, c.Pos.Z) {
				continue
			}
			w.chunks[c.Pos.Key()] = c
			// Mark existing XZ neighbours dirty so their border faces update
			// (a previously-air neighbour edge is now occluded).
			for _, off := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				w.markDirty(c.Pos.X+off[0], c.Pos.Y, c.Pos.Z+off[1])
			}
		default:
			return
		}
	}
}

// enqueueNeeded submits missing chunks within the render distance for
// background generation.
func (w *World) enqueueNeeded() {
	px, pz := w.playerChunk.X, w.playerChunk.Z
	for dz := -int32(w.renderDist); dz <= int32(w.renderDist); dz++ {
		for dx := -int32(w.renderDist); dx <= int32(w.renderDist); dx++ {
			for cy := MinChunkY; cy <= MaxChunkY; cy++ {
				pos := ChunkPos{px + dx, cy, pz + dz}
				key := pos.Key()
				if _, ok := w.chunks[key]; ok {
					continue
				}
				if w.queued[key] {
					continue
				}
				select {
				case w.genQueue <- pos:
					w.queued[key] = true
				default:
					// Queue full; retry next frame.
				}
			}
		}
	}
}

// unloadDistant removes chunks beyond the render distance plus a margin and
// records them so the renderer can free their GPU buffers.
func (w *World) unloadDistant() {
	limit := int32(w.renderDist) + 1
	for key, c := range w.chunks {
		if abs32(c.Pos.X-w.playerChunk.X) > limit || abs32(c.Pos.Z-w.playerChunk.Z) > limit {
			delete(w.chunks, key)
			delete(w.queued, key)
			w.pendingUnloads = append(w.pendingUnloads, c.Pos)
		}
	}
}

// meshDirty builds meshes for the closest ready dirty chunks, up to the
// per-frame budget, and stages them for the renderer to upload.
func (w *World) meshDirty() {
	if len(w.chunks) == 0 {
		return
	}
	type candidate struct {
		pos   ChunkPos
		dist2 int32
	}
	cands := make([]candidate, 0, 64)
	for _, c := range w.chunks {
		if !c.dirty {
			continue
		}
		if !w.isMeshReady(c) {
			continue
		}
		dx := c.Pos.X - w.playerChunk.X
		dz := c.Pos.Z - w.playerChunk.Z
		cands = append(cands, candidate{c.Pos, dx*dx + dz*dz})
	}
	if len(cands) == 0 {
		return
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].dist2 < cands[j].dist2 })

	n := len(cands)
	if n > w.meshPerFrame {
		n = w.meshPerFrame
	}
	for i := 0; i < n; i++ {
		pos := cands[i].pos
		c := w.chunks[pos.Key()]
		if c == nil {
			continue
		}
		data := BuildChunkMesh(c, w.Sampler(), w.atlas)
		c.MarkClean()
		w.pendingUploads = append(w.pendingUploads, MeshUpload{Pos: pos, Data: data})
	}
}

// isMeshReady reports whether a chunk's neighbourhood is stable enough to mesh
// without immediately becoming stale. A chunk is ready when every in-range XZ
// neighbour has been generated; out-of-range neighbours are treated as air.
func (w *World) isMeshReady(c *Chunk) bool {
	for _, off := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, nz := c.Pos.X+off[0], c.Pos.Z+off[1]
		if !w.inRangeXZ(nx, nz) {
			continue
		}
		if _, ok := w.chunks[(ChunkPos{nx, c.Pos.Y, nz}).Key()]; !ok {
			return false
		}
	}
	return true
}

// inRangeXZ reports whether a chunk column is within the render distance of the
// player (so it will be loaded and should be waited for).
func (w *World) inRangeXZ(cx, cz int32) bool {
	return abs32(cx-w.playerChunk.X) <= int32(w.renderDist) &&
		abs32(cz-w.playerChunk.Z) <= int32(w.renderDist)
}

// markDirty flags the chunk at the given coordinate for remeshing, if present.
func (w *World) markDirty(cx, cy, cz int32) {
	c, ok := w.chunks[(ChunkPos{cx, cy, cz}).Key()]
	if !ok {
		return
	}
	c.dirty = true
}

// Sampler returns a BlockSampler that reads across chunk boundaries. Safe to
// call from the main thread (meshing, raycasting).
func (w *World) Sampler() BlockSampler {
	return func(wx, wy, wz int32) BlockID {
		return w.GetBlock(wx, wy, wz)
	}
}

// GetBlock returns the block at a world-space block coordinate, or Air if the
// containing chunk is not loaded.
func (w *World) GetBlock(wx, wy, wz int32) BlockID {
	cx, cy, cz := worldToChunk(wx, wy, wz)
	c, ok := w.chunks[(ChunkPos{cx, cy, cz}).Key()]
	if !ok {
		return Air
	}
	lx, ly, lz := worldToLocal(wx, wy, wz)
	return c.GetLocal(lx, ly, lz)
}

// SetBlock sets the block at a world-space coordinate and marks the affected
// chunk (and any chunk sharing the edited border face) for remeshing. If the
// chunk is not loaded, the call is a no-op.
func (w *World) SetBlock(wx, wy, wz int32, b BlockID) {
	cx, cy, cz := worldToChunk(wx, wy, wz)
	c, ok := w.chunks[(ChunkPos{cx, cy, cz}).Key()]
	if !ok {
		return
	}
	lx, ly, lz := worldToLocal(wx, wy, wz)
	c.SetLocal(lx, ly, lz, b)
	// Flag neighbours that share the edited face so their border meshes update.
	if lx == 0 {
		w.markDirty(cx-1, cy, cz)
	}
	if lx == ChunkSize-1 {
		w.markDirty(cx+1, cy, cz)
	}
	if ly == 0 {
		w.markDirty(cx, cy-1, cz)
	}
	if ly == ChunkSize-1 {
		w.markDirty(cx, cy+1, cz)
	}
	if lz == 0 {
		w.markDirty(cx, cy, cz-1)
	}
	if lz == ChunkSize-1 {
		w.markDirty(cx, cy, cz+1)
	}
}

// DrainUploads returns and clears the list of chunk meshes staged for upload.
// The renderer calls this once per frame.
func (w *World) DrainUploads() []MeshUpload {
	out := w.pendingUploads
	w.pendingUploads = nil
	return out
}

// DrainUnloads returns and clears the list of chunks that were unloaded and
// whose GPU buffers the renderer should release.
func (w *World) DrainUnloads() []ChunkPos {
	out := w.pendingUnloads
	w.pendingUnloads = nil
	return out
}

// ChunkAABB returns the world-space AABB for a chunk position, for frustum
// culling on the renderer side.
func ChunkAABB(pos ChunkPos) (minX, minY, minZ, maxX, maxY, maxZ float32) {
	ox, oy, oz := pos.WorldOrigin()
	return float32(ox), float32(oy), float32(oz),
		float32(ox + ChunkSize), float32(oy + ChunkSize), float32(oz + ChunkSize)
}

// worldToChunk converts a world block coordinate to its containing chunk coord.
// Uses floor division so negative coordinates map correctly.
func worldToChunk(wx, wy, wz int32) (int32, int32, int32) {
	return floorDiv(wx, ChunkSize), floorDiv(wy, ChunkSize), floorDiv(wz, ChunkSize)
}

// worldToLocal converts a world block coordinate to its local coordinate within
// the containing chunk ([0, ChunkSize-1]).
func worldToLocal(wx, wy, wz int32) (int, int, int) {
	return int(floorMod(wx, ChunkSize)), int(floorMod(wy, ChunkSize)), int(floorMod(wz, ChunkSize))
}

func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int32) int32 {
	r := a % b
	if r != 0 && ((r < 0) != (b < 0)) {
		r += b
	}
	return r
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
