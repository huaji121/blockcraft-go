// Package player implements the first-person controller: a quaternion-based
// free-look camera and a voxel-aware physics body with per-axis collision.
package player

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"blockcraft-go/internal/world"
)

// Movement tuning constants.
const (
	defaultMoveSpeed   = 4.317 // blocks/sec, ~Minecraft walk speed
	defaultSprintSpeed = 5.6
	jumpSpeed          = 8.0
	gravity            = 22.0

	eyeHeight   = 1.62
	bodyHeight  = 1.8
	bodyRadius  = 0.3
	maxPitch    = math.Pi/2 - 0.01 // just under 90° to avoid gimbal lock
	mouseSens   = 0.0022
	// Reach is the maximum distance (in blocks) at which the player can
	// interact with blocks via the camera ray.
	Reach = 6.0
)

// Input is the per-frame input state fed to Player.Update.
type Input struct {
	Forward, Backward, Left, Right bool
	Jump, Crouch                   bool
	Sprint                         bool
}

// Player is the first-person camera/body.
type Player struct {
	Pos      mgl32.Vec3
	Vel      mgl32.Vec3
	// PrevPos is the position at the start of the most recent physics tick; the
	// renderer interpolates between PrevPos and Pos to keep motion smooth at
	// render rates higher than the physics rate.
	PrevPos   mgl32.Vec3
	Yaw       float32 // radians around world Y
	Pitch     float32 // radians around local X, clamped to ±maxPitch
	OnGround  bool
	Flying    bool

	// FlyToggle is a rising-edge signal set by the app when the fly key is
	// pressed; consumed each update.
	FlyToggle bool

	MoveSpeed float32
}

// NewPlayer creates a player at the given position, looking along -Z.
func NewPlayer(pos mgl32.Vec3) *Player {
	return &Player{
		Pos:       pos,
		PrevPos:   pos,
		Yaw:       0,
		Pitch:     0,
		MoveSpeed: defaultMoveSpeed,
	}
}

// Orientation returns the camera orientation as a quaternion, composed from yaw
// (around world up) then pitch (around local right). Composing rotations as
// quaternions — rather than accumulating Euler matrices — keeps the orientation
// orthonormal, and clamping pitch below 90° avoids the gimbal-lock singularity
// that an Euler YXZ sequence hits at the poles.
func (p *Player) Orientation() mgl32.Quat {
	yaw := mgl32.QuatRotate(p.Yaw, mgl32.Vec3{0, 1, 0})
	pitch := mgl32.QuatRotate(p.Pitch, mgl32.Vec3{1, 0, 0})
	return yaw.Mul(pitch)
}

// Forward returns the unit direction the camera looks along.
func (p *Player) Forward() mgl32.Vec3 {
	return p.Orientation().Rotate(mgl32.Vec3{0, 0, -1})
}

// ForwardHoriz returns the camera forward projected onto the horizontal plane
// and normalised, used for WASD movement so looking up/down doesn't slow you.
func (p *Player) ForwardHoriz() mgl32.Vec3 {
	f := p.Forward()
	f[1] = 0
	return f.Normalize()
}

// Right returns the camera's horizontal right vector.
func (p *Player) Right() mgl32.Vec3 {
	return p.ForwardHoriz().Cross(mgl32.Vec3{0, 1, 0}).Normalize()
}

// EyePosition returns the world position of the camera (feet + eyeHeight).
func (p *Player) EyePosition() mgl32.Vec3 {
	return p.Pos.Add(mgl32.Vec3{0, eyeHeight, 0})
}

// InterpolatedEye returns the camera position linearly blended between the
// previous and current physics tick positions by alpha in [0,1]. Used by the
// renderer so motion stays smooth when the render rate exceeds the 60 Hz
// physics rate.
func (p *Player) InterpolatedEye(alpha float32) mgl32.Vec3 {
	pos := p.PrevPos.Add(p.Pos.Sub(p.PrevPos).Mul(alpha))
	return pos.Add(mgl32.Vec3{0, eyeHeight, 0})
}

// ViewMatrix returns the camera's view matrix at the current physics position.
func (p *Player) ViewMatrix() mgl32.Mat4 {
	return p.ViewMatrixFrom(p.EyePosition())
}

// ViewMatrixFrom returns the camera's view matrix centred on the given eye
// position, using the current orientation. Pass an interpolated eye for smooth
// rendering between physics ticks.
func (p *Player) ViewMatrixFrom(eye mgl32.Vec3) mgl32.Mat4 {
	center := eye.Add(p.Forward())
	return mgl32.LookAt(eye.X(), eye.Y(), eye.Z(), center.X(), center.Y(), center.Z(), 0, 1, 0)
}

// HandleMouse applies relative mouse motion to yaw and pitch.
//
// SDL reports xrel>0 when the mouse moves right and yrel>0 when it moves down
// (screen Y grows downward). With mgl32's right-handed QuatRotate, a positive
// yaw rotates the forward vector toward -X (left) and a positive pitch toward
// +Y (up), so we subtract xrel (mouse right → look right) and subtract yrel
// (mouse down → look down, standard non-inverted).
func (p *Player) HandleMouse(xrel, yrel float32) {
	p.Yaw -= xrel * mouseSens
	p.Pitch -= yrel * mouseSens
	// Wrap yaw into [-pi, pi] for numerical stability.
	if p.Yaw > math.Pi {
		p.Yaw -= 2 * math.Pi
	} else if p.Yaw < -math.Pi {
		p.Yaw += 2 * math.Pi
	}
	if p.Pitch > maxPitch {
		p.Pitch = maxPitch
	} else if p.Pitch < -maxPitch {
		p.Pitch = -maxPitch
	}
}

// Update integrates the player's physics for one frame. Collision is resolved
// per-axis against the voxel grid, which gives smooth wall-sliding and ground
// standing without a full physics engine.
func (p *Player) Update(dt float32, in Input, w *world.World) {
	if p.FlyToggle {
		p.Flying = !p.Flying
		p.FlyToggle = false
		p.Vel = mgl32.Vec3{}
	}

	// Horizontal wish-direction from input, in camera space.
	speed := p.MoveSpeed
	if in.Sprint {
		speed = defaultSprintSpeed
	}
	var move mgl32.Vec3
	if in.Forward {
		move = move.Add(p.ForwardHoriz())
	}
	if in.Backward {
		move = move.Sub(p.ForwardHoriz())
	}
	if in.Right {
		move = move.Add(p.Right())
	}
	if in.Left {
		move = move.Sub(p.Right())
	}
	if move.LenSqr() > 0 {
		move = move.Normalize().Mul(speed)
	}

	if p.Flying {
		// Free flight: full 6-DOF, no gravity.
		p.Vel[0] = move.X()
		p.Vel[2] = move.Z()
		vy := float32(0)
		if in.Jump {
			vy += speed
		}
		if in.Crouch {
			vy -= speed
		}
		p.Vel[1] = vy
		p.moveAxis(w, dt, 0)
		p.moveAxis(w, dt, 1)
		p.moveAxis(w, dt, 2)
		return
	}

	// Walking: horizontal velocity follows input directly (simple accel), and
	// gravity acts on Y.
	p.Vel[0] = move.X()
	p.Vel[2] = move.Z()
	p.Vel[1] -= gravity * dt
	if in.Jump && p.OnGround {
		p.Vel[1] = jumpSpeed
		p.OnGround = false
	}

	p.OnGround = false
	p.moveAxis(w, dt, 0)
	p.moveAxis(w, dt, 2)
	p.moveAxis(w, dt, 1)
}

// moveAxis moves the player along one axis and resolves collisions against the
// voxel grid by reverting that axis' motion when a solid block is encountered.
// Resolving per-axis (rather than all at once) is what produces wall-sliding.
func (p *Player) moveAxis(w *world.World, dt float32, axis int) {
	p.Pos[axis] += p.Vel[axis] * dt
	if p.collides(w) {
		// Revert and zero the velocity on this axis.
		p.Pos[axis] -= p.Vel[axis] * dt
		if axis == 1 && p.Vel[1] < 0 {
			p.OnGround = true
		}
		p.Vel[axis] = 0
	}
}

// collides reports whether the player's body AABB overlaps any solid block.
func (p *Player) collides(w *world.World) bool {
	minX := int32(math.Floor(float64(p.Pos.X() - bodyRadius)))
	maxX := int32(math.Floor(float64(p.Pos.X() + bodyRadius)))
	minY := int32(math.Floor(float64(p.Pos.Y())))
	maxY := int32(math.Floor(float64(p.Pos.Y() + bodyHeight)))
	minZ := int32(math.Floor(float64(p.Pos.Z() - bodyRadius)))
	maxZ := int32(math.Floor(float64(p.Pos.Z() + bodyRadius)))
	for y := minY; y <= maxY; y++ {
		for z := minZ; z <= maxZ; z++ {
			for x := minX; x <= maxX; x++ {
				if !w.GetBlock(x, y, z).IsAir() {
					return true
				}
			}
		}
	}
	return false
}

// Ray returns the origin and direction of the block-interaction ray, emanating
// from the camera eye along the view direction.
func (p *Player) Ray() (mgl32.Vec3, mgl32.Vec3) {
	return p.EyePosition(), p.Forward()
}
