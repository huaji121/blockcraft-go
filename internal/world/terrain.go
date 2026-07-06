package world

import (
	"math"
)

// World vertical extent, in chunk coordinates. Chunks are only generated for
// MinChunkY <= Y <= MaxChunkY. With ChunkSize=32 this gives a 64 block tall
// world, enough for a surface layer (chunk Y=1) and an underground layer
// (chunk Y=0) for the player to dig into.
const (
	MinChunkY int32 = 0
	MaxChunkY int32 = 1
)

// surfaceBaseY is the approximate world Y of the terrain surface around the
// spawn area. The actual height per column varies with noise around this.
const surfaceBaseY = 34

// heightAt returns the terrain surface height (top solid block Y + 1) for a
// world-space column using a small two-octave value noise derived from seed.
// It is deterministic so chunks regenerate identically after unload/reload.
func heightAt(wx, wz int32, seed int64) int32 {
	noise := func(x, z int) float32 {
		// Hash two integers + seed into [0,1).
		h := hash3(int64(x), int64(z), seed)
		return float32(h) / float32(math.MaxUint32)
	}
	smooth := func(x, z float32) float32 {
		ix := int(math.Floor(float64(x)))
		iz := int(math.Floor(float64(z)))
		fx := x - float32(ix)
		fz := z - float32(iz)
		// Smoothstep for nicer interpolation.
		sx := fx * fx * (3 - 2*fx)
		sz := fz * fz * (3 - 2*fz)
		v00 := noise(ix, iz)
		v10 := noise(ix+1, iz)
		v01 := noise(ix, iz+1)
		v11 := noise(ix+1, iz+1)
		a := v00 + (v10-v00)*sx
		b := v01 + (v11-v01)*sx
		return a + (b-a)*sz
	}

	const scale1 = 0.0625 // 1/16 — large rolling hills
	const scale2 = 0.125  // 1/8  — medium detail
	x := float32(wx)
	z := float32(wz)
	h := smooth(x*scale1, z*scale1)*0.7 + smooth(x*scale2, z*scale2)*0.3
	// Map [0,1] to a height range around surfaceBaseY.
	return int32(surfaceBaseY) + int32((h-0.5)*16)
}

// generateTerrain fills a chunk's block data with the procedural terrain.
// Stone under dirt under grass on the surface, bedrock at the very bottom.
func generateTerrain(c *Chunk, seed int64) {
	ox, oy, oz := c.Pos.WorldOrigin()
	hasSolid := false
	for lz := 0; lz < ChunkSize; lz++ {
		for lx := 0; lx < ChunkSize; lx++ {
			wx := ox + int32(lx)
			wz := oz + int32(lz)
			h := heightAt(wx, wz, seed)
			for ly := 0; ly < ChunkSize; ly++ {
				wy := oy + int32(ly)
				var b BlockID
				switch {
				case wy == 0:
					b = Bedrock
				case wy < h-4:
					b = Stone
				case wy < h-1:
					b = Dirt
				case wy == h-1:
					b = Grass
				default:
					b = Air
				}
				// Sprinkle a little ore in the stone layer so digging is
				// mildly rewarding.
				if b == Stone {
					b = oreAt(wx, wy, wz, seed)
				}
				if b != Air {
					hasSolid = true
				}
				c.Blocks[index(lx, ly, lz)] = b
			}
		}
	}
	c.empty = !hasSolid
}

// oreAt returns an ore block type for the given world position, or Stone if no
// ore should be placed. Probabilities are tiny so ore is rare.
func oreAt(wx, wy, wz int32, seed int64) BlockID {
	h := hash3(int64(wx), int64(wy)*2654435761, int64(wz)^seed)
	r := h % 1000
	switch {
	case r < 4 && wy < 20:
		return DiamondOre
	case r < 12 && wy < 30:
		return GoldOre
	case r < 30:
		return IronOre
	case r < 60:
		return CoalOre
	default:
		return Stone
	}
}

// hash3 mixes three int64 values into a uint32 hash. Uses splitmix64-style
// mixing for decent distribution; good enough for terrain.
func hash3(a, b, c int64) uint32 {
	x := uint64(a)*6364136223846793005 + uint64(b)*1442695040888963407 + uint64(c)*2862933555777941757 + 0x9E3779B97F4A7C15
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return uint32(x)
}
