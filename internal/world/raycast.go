package world

import "math"

// RayHit describes the result of a voxel raycast.
type RayHit struct {
	Hit                  bool
	BlockX, BlockY, BlockZ int32 // world block coordinate of the struck block
	Face                 int    // face that was entered (FacePosX..FaceNegZ), or -1
}

// AdjacentBlock returns the world block coordinate where a new block would be
// placed after a hit — i.e. on the empty side of the struck face. Returns the
// hit block itself when no face was struck.
func (r RayHit) AdjacentBlock() (int32, int32, int32) {
	if !r.Hit || r.Face < 0 {
		return r.BlockX, r.BlockY, r.BlockZ
	}
	off := faceOffsets[r.Face]
	return r.BlockX + off[0], r.BlockY + off[1], r.BlockZ + off[2]
}

// RaycastVoxel marches a ray through the voxel grid using the Amanatides & Woo
// fast voxel traversal algorithm and returns the first non-air block it hits.
//
//	origin – ray origin in world space (block units)
//	dir    – ray direction; it is normalised inside the function
//	maxDist– maximum travel distance in block units
//	sample – block lookup crossing chunk boundaries
func RaycastVoxel(ox, oy, oz, dx, dy, dz, maxDist float32, sample BlockSampler) RayHit {
	// Normalise the direction.
	invLen := 1.0 / float32(math.Sqrt(float64(dx*dx+dy*dy+dz*dz)))
	if invLen == 0 || math.IsNaN(float64(invLen)) {
		return RayHit{Hit: false, Face: -1}
	}
	dx *= invLen
	dy *= invLen
	dz *= invLen

	// Current voxel coordinates.
	ix := int32(math.Floor(float64(ox)))
	iy := int32(math.Floor(float64(oy)))
	iz := int32(math.Floor(float64(oz)))

	// Step direction per axis.
	var stepX, stepY, stepZ int32
	if dx > 0 {
		stepX = 1
	} else if dx < 0 {
		stepX = -1
	}
	if dy > 0 {
		stepY = 1
	} else if dy < 0 {
		stepY = -1
	}
	if dz > 0 {
		stepZ = 1
	} else if dz < 0 {
		stepZ = -1
	}

	// tMax: distance along the ray to the next voxel boundary on each axis.
	// tDelta: distance between successive voxel boundaries on each axis.
	const inf = float32(math.MaxFloat32)

	tMaxX := inf
	tMaxY := inf
	tMaxZ := inf
	tDeltaX := inf
	tDeltaY := inf
	tDeltaZ := inf

	if dx != 0 {
		boundary := float32(ix)
		if stepX > 0 {
			boundary = float32(ix + 1)
		}
		tMaxX = (boundary - ox) / dx
		tDeltaX = float32(math.Abs(1.0 / float64(dx)))
	}
	if dy != 0 {
		boundary := float32(iy)
		if stepY > 0 {
			boundary = float32(iy + 1)
		}
		tMaxY = (boundary - oy) / dy
		tDeltaY = float32(math.Abs(1.0 / float64(dy)))
	}
	if dz != 0 {
		boundary := float32(iz)
		if stepZ > 0 {
			boundary = float32(iz + 1)
		}
		tMaxZ = (boundary - oz) / dz
		tDeltaZ = float32(math.Abs(1.0 / float64(dz)))
	}

	// Check the starting voxel first (the camera may be inside a block).
	face := -1
	for {
		b := sample(ix, iy, iz)
		if !b.IsAir() {
			return RayHit{Hit: true, BlockX: ix, BlockY: iy, BlockZ: iz, Face: face}
		}

		// Advance to the nearest voxel boundary.
		switch {
		case tMaxX < tMaxY && tMaxX < tMaxZ:
			if tMaxX > maxDist {
				return RayHit{Hit: false, Face: -1}
			}
			ix += stepX
			tMaxX += tDeltaX
			if stepX > 0 {
				face = FaceNegX // entered new voxel through its -X face
			} else {
				face = FacePosX
			}
		case tMaxY < tMaxZ:
			if tMaxY > maxDist {
				return RayHit{Hit: false, Face: -1}
			}
			iy += stepY
			tMaxY += tDeltaY
			if stepY > 0 {
				face = FaceNegY
			} else {
				face = FacePosY
			}
		default:
			if tMaxZ > maxDist {
				return RayHit{Hit: false, Face: -1}
			}
			iz += stepZ
			tMaxZ += tDeltaZ
			if stepZ > 0 {
				face = FaceNegZ
			} else {
				face = FacePosZ
			}
		}
	}
}
