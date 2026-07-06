// Package app wires together the SDL3 window, the GPU renderer, the voxel world
// and the player into the main game loop.
package app

import (
	"fmt"
	"math"
	"time"

	"github.com/Zyko0/go-sdl3/bin/binsdl"
	"github.com/Zyko0/go-sdl3/sdl"
	"github.com/go-gl/mathgl/mgl32"

	"blockcraft-go/internal/player"
	"blockcraft-go/internal/renderer"
	"blockcraft-go/internal/world"
)

// Config holds the launch-time configuration of the game.
type Config struct {
	WindowWidth  int
	WindowHeight int
	RenderDist   int // chunk radius
	Seed         int64
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		WindowWidth:  1280,
		WindowHeight: 720,
		RenderDist:   5,
		Seed:         1337,
	}
}

const (
	windowTitle = "Blockcraft"
	tickRate    = 1.0 / 60.0 // fixed physics timestep
	fovY        = 70.0 * math.Pi / 180.0
	nearPlane   = 0.1
	farPlane    = 512.0
)

// App is the running game instance.
type App struct {
	cfg      Config
	device   *sdl.GPUDevice
	window   *sdl.Window
	renderer *renderer.Renderer
	world    *world.World
	player   *player.Player

	running      bool
	mouseGrabbed bool

	// Accumulated mouse motion since the last frame, applied to the player
	// before physics.
	mouseDX, mouseDY float32

	// FPS accounting for the window title.
	fpsAccum  float32
	fpsFrames int
}

// Run initialises SDL/GPU/world/player and drives the main loop until the
// window is closed.
func Run(cfg Config) error {
	a := &App{cfg: cfg, mouseGrabbed: true}

	// Load the embedded SDL3 library and initialise the video subsystem.
	defer binsdl.Load().Unload()
	defer sdl.Quit()
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return fmt.Errorf("sdl init: %w", err)
	}

	// Create the GPU device and window, then bind them.
	device, err := sdl.CreateGPUDevice(
		sdl.GPU_SHADERFORMAT_SPIRV|sdl.GPU_SHADERFORMAT_DXIL|sdl.GPU_SHADERFORMAT_MSL,
		false,
		"",
	)
	if err != nil {
		return fmt.Errorf("create gpu device: %w", err)
	}
	a.device = device

	window, err := sdl.CreateWindow(windowTitle, cfg.WindowWidth, cfg.WindowHeight, sdl.WINDOW_RESIZABLE)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	a.window = window
	if err := device.ClaimWindow(window); err != nil {
		return fmt.Errorf("claim window: %w", err)
	}

	// Build the renderer (atlas, pipeline, depth target).
	r, err := renderer.NewRenderer(device, window)
	if err != nil {
		return fmt.Errorf("renderer init: %w", err)
	}
	a.renderer = r

	// World + player. The atlas implements world.AtlasUVProvider.
	w := world.NewWorld(cfg.Seed, cfg.RenderDist, r.Atlas())
	w.Start(2)
	a.world = w

	spawn := mgl32.Vec3{0.5, 50, 0.5}
	w.Prefill(spawn)
	a.player = player.NewPlayer(spawn)

	// Capture the mouse for first-person look.
	if err := window.SetRelativeMouseMode(true); err != nil {
		return fmt.Errorf("set relative mouse: %w", err)
	}

	a.running = true
	if err := a.loop(); err != nil {
		return err
	}

	// Tear down.
	w.Close()
	a.renderer.Destroy()
	device.ReleaseWindow(window)
	window.Destroy()
	device.Destroy()
	return nil
}

// loop is the main frame loop. It uses a fixed-timestep accumulator for physics
// so movement is stable at 60 Hz regardless of the render rate, while rendering
// happens once per frame (capped by the swapchain's vsync).
func (a *App) loop() error {
	last := time.Now()
	for a.running {
		now := time.Now()
		dt := float32(now.Sub(last).Seconds())
		last = now
		// Clamp dt to avoid a huge catch-up spiral after a stall (e.g. on
		// startup or when the window is dragged).
		if dt > 0.25 {
			dt = 0.25
		}

		a.handleEvents()

		// Apply accumulated mouse motion to the camera.
		if a.mouseGrabbed {
			a.player.HandleMouse(a.mouseDX, a.mouseDY)
		}
		a.mouseDX, a.mouseDY = 0, 0

		// Fixed-timestep physics.
		in := a.readInput()
		accumulator := dt
		for accumulator >= tickRate {
			a.player.Update(tickRate, in, a.world)
			accumulator -= tickRate
		}
		// Keep the world's chunk loading in sync with the player position.
		a.world.Update(a.player.EyePosition())

		// Push any freshly built chunk meshes to the GPU.
		a.renderer.ApplyWorldEvents(a.world)

		// Build the view-projection matrix and render.
		aspect := a.aspect()
		proj := mgl32.Perspective(fovY, aspect, nearPlane, farPlane)
		view := a.player.ViewMatrix()
		viewproj := proj.Mul4(view)
		if err := a.renderer.Render(viewproj); err != nil {
			return fmt.Errorf("render: %w", err)
		}

		a.updateFPS(dt)
	}
	return nil
}

// handleEvents polls the SDL event queue for one frame's worth of input.
func (a *App) handleEvents() {
	var event sdl.Event
	for sdl.PollEvent(&event) {
		switch event.Type {
		case sdl.EVENT_QUIT, sdl.EVENT_WINDOW_CLOSE_REQUESTED:
			a.running = false
		case sdl.EVENT_KEY_DOWN:
			ke := event.KeyboardEvent()
			if ke.Repeat {
				continue
			}
			switch ke.Key {
			case sdl.K_ESCAPE:
				// Toggle mouse capture: first press releases the cursor, a
				// click on the window re-captures it.
				a.setMouseGrabbed(!a.mouseGrabbed)
			case sdl.K_F:
				if a.mouseGrabbed {
					a.player.FlyToggle = true
				}
			}
		case sdl.EVENT_MOUSE_MOTION:
			if a.mouseGrabbed {
				me := event.MouseMotionEvent()
				a.mouseDX += me.Xrel
				a.mouseDY += me.Yrel
			}
		case sdl.EVENT_MOUSE_BUTTON_DOWN:
			mb := event.MouseButtonEvent()
			if !a.mouseGrabbed {
				// Re-capture on any click while paused.
				a.setMouseGrabbed(true)
				continue
			}
			// MouseButtonEvent.Button is a uint8 index; the sdl.BUTTON_*
			// constants are MouseButtonFlags, so cast for the comparison.
			switch sdl.MouseButtonFlags(mb.Button) {
			case sdl.BUTTON_LEFT:
				a.breakBlock()
			case sdl.BUTTON_RIGHT:
				a.placeBlock()
			}
		}
	}
}

// readInput builds the per-frame Input struct from the keyboard snapshot.
func (a *App) readInput() player.Input {
	if !a.mouseGrabbed {
		return player.Input{}
	}
	keys := sdl.GetKeyboardState()
	return player.Input{
		Forward:  keys[sdl.SCANCODE_W],
		Backward: keys[sdl.SCANCODE_S],
		Left:     keys[sdl.SCANCODE_A],
		Right:    keys[sdl.SCANCODE_D],
		Jump:     keys[sdl.SCANCODE_SPACE],
		Crouch:   keys[sdl.SCANCODE_LSHIFT] || keys[sdl.SCANCODE_RSHIFT],
		Sprint:   keys[sdl.SCANCODE_LCTRL] || keys[sdl.SCANCODE_RCTRL],
	}
}

// breakBlock removes the block hit by the camera ray.
func (a *App) breakBlock() {
	origin, dir := a.player.Ray()
	hit := world.RaycastVoxel(origin.X(), origin.Y(), origin.Z(), dir.X(), dir.Y(), dir.Z(), player.Reach, a.world.Sampler())
	if hit.Hit {
		a.world.SetBlock(hit.BlockX, hit.BlockY, hit.BlockZ, world.Air)
	}
}

// placeBlock puts a stone block adjacent to the hit face, unless the target
// cell is occupied by the player's body.
func (a *App) placeBlock() {
	origin, dir := a.player.Ray()
	hit := world.RaycastVoxel(origin.X(), origin.Y(), origin.Z(), dir.X(), dir.Y(), dir.Z(), player.Reach, a.world.Sampler())
	if !hit.Hit {
		return
	}
	x, y, z := hit.AdjacentBlock()
	if a.blockIntersectsPlayer(x, y, z) {
		return
	}
	a.world.SetBlock(x, y, z, world.Stone)
}

// blockIntersectsPlayer reports whether placing a block at (x,y,z) would
// overlap the player's body AABB (so you can't trap yourself in stone).
func (a *App) blockIntersectsPlayer(x, y, z int32) bool {
	const (
		radius = 0.3
		height = 1.8
	)
	p := a.player.Pos
	minBX, maxBX := float32(x), float32(x+1)
	minBY, maxBY := float32(y), float32(y+1)
	minBZ, maxBZ := float32(z), float32(z+1)
	pMinX, pMaxX := p.X()-radius, p.X()+radius
	pMinY, pMaxY := p.Y(), p.Y()+height
	pMinZ, pMaxZ := p.Z()-radius, p.Z()+radius
	return pMaxX > minBX && pMinX < maxBX &&
		pMaxY > minBY && pMinY < maxBY &&
		pMaxZ > minBZ && pMinZ < maxBZ
}

// setMouseGrabbed toggles relative mouse mode and cursor visibility.
func (a *App) setMouseGrabbed(grabbed bool) {
	a.mouseGrabbed = grabbed
	_ = a.window.SetRelativeMouseMode(grabbed)
}

// aspect returns the current framebuffer aspect ratio.
func (a *App) aspect() float32 {
	w, h, err := a.window.SizeInPixels()
	if err != nil || h == 0 {
		return float32(a.cfg.WindowWidth) / float32(a.cfg.WindowHeight)
	}
	return float32(w) / float32(h)
}

// updateFPS updates the window title with the current FPS roughly twice a
// second. It is best-effort and never fails the loop.
func (a *App) updateFPS(dt float32) {
	a.fpsAccum += dt
	a.fpsFrames++
	if a.fpsAccum >= 0.5 {
		fps := float32(a.fpsFrames) / a.fpsAccum
		_ = a.window.SetTitle(fmt.Sprintf("%s — %.0f FPS", windowTitle, fps))
		a.fpsAccum = 0
		a.fpsFrames = 0
	}
}
