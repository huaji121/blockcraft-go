// Package math3d holds the small bits of 3D math used across the game that are
// not directly provided by mgl32: axis-aligned bounding boxes and a view
// frustum extracted from a view-projection matrix for chunk culling.
package math3d

import (
	"github.com/go-gl/mathgl/mgl32"
)

// AABB is an axis-aligned bounding box in world space.
type AABB struct {
	Min mgl32.Vec3
	Max mgl32.Vec3
}

// Corners returns the 8 corners of the box, in the order SDL_gpu / frustum
// tests expect (all combinations of min/max on each axis).
func (b AABB) Corners() [8]mgl32.Vec3 {
	mn, mx := b.Min, b.Max
	return [8]mgl32.Vec3{
		{mn.X(), mn.Y(), mn.Z()},
		{mx.X(), mn.Y(), mn.Z()},
		{mx.X(), mx.Y(), mn.Z()},
		{mn.X(), mx.Y(), mn.Z()},
		{mn.X(), mn.Y(), mx.Z()},
		{mx.X(), mn.Y(), mx.Z()},
		{mx.X(), mx.Y(), mx.Z()},
		{mn.X(), mx.Y(), mx.Z()},
	}
}

// Frustum is the six half-space planes defining the camera's view volume.
// Each plane is stored as a Vec4 where xyz is the normal and w is the
// distance such that a point p is inside the frustum when dot(plane, p) >= 0.
type Frustum [6]mgl32.Vec4

// FrustumFromMatrix extracts the view frustum planes from a combined
// view-projection matrix. The matrix is assumed to be column-major (mgl32's
// default layout) and is applied as m * p, matching how the renderer uploads it
// to the shader. Planes follow the Gribb-Hartmann extraction using the matrix
// rows (mgl32's Row returns the math row of a column-major matrix).
func FrustumFromMatrix(m mgl32.Mat4) Frustum {
	r0 := m.Row(0)
	r1 := m.Row(1)
	r2 := m.Row(2)
	r3 := m.Row(3)

	// plane normalises (a,b,c) so the signed-distance test is well-scaled; the
	// sign — which is all the inside/outside test cares about — is preserved.
	plane := func(a, b, c, d float32) mgl32.Vec4 {
		n := mgl32.Vec3{a, b, c}
		l := n.Len()
		if l == 0 {
			return mgl32.Vec4{a, b, c, d}
		}
		return mgl32.Vec4{a / l, b / l, c / l, d / l}
	}

	return Frustum{
		plane(r3[0]+r0[0], r3[1]+r0[1], r3[2]+r0[2], r3[3]+r0[3]), // left
		plane(r3[0]-r0[0], r3[1]-r0[1], r3[2]-r0[2], r3[3]-r0[3]), // right
		plane(r3[0]+r1[0], r3[1]+r1[1], r3[2]+r1[2], r3[3]+r1[3]), // bottom
		plane(r3[0]-r1[0], r3[1]-r1[1], r3[2]-r1[2], r3[3]-r1[3]), // top
		plane(r3[0]+r2[0], r3[1]+r2[1], r3[2]+r2[2], r3[3]+r2[3]), // near
		plane(r3[0]-r2[0], r3[1]-r2[1], r3[2]-r2[2], r3[3]-r2[3]), // far
	}
}

// Intersects reports whether the AABB is at least partially inside the frustum.
// A chunk whose AABB is fully outside any single plane is culled; otherwise it
// is considered visible (conservative — we don't do the more expensive
// edge/vertex exact tests, which is the standard trade-off for voxel chunks).
func (f Frustum) Intersects(box AABB) bool {
	corners := box.Corners()
	for _, plane := range f {
		out := 0
		for _, c := range corners {
			// A corner is outside this plane when the signed distance is < 0.
			if plane.X()*c.X()+plane.Y()*c.Y()+plane.Z()*c.Z()+plane.W() < 0 {
				out++
			}
		}
		// All 8 corners outside the same plane => the box is fully outside.
		if out == len(corners) {
			return false
		}
	}
	return true
}
