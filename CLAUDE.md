# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this repository.

## Build & run

```bash
go build -o blockcraft.exe ./cmd/blockcraft   # build only
./blockcraft.exe                                 # run directly
./start.sh                                       # build + run (auto-picks blockcraft/blockcraft.exe)
./start.sh --no-build                            # run last artifact without rebuilding
```

Requires **Go 1.26+**. The SDL3 shared library is embedded by `go-sdl3/bin/binsdl` — no system SDL3 install needed. The codebase has **no automated tests** (`go test ./...` finds none). Runtime verification is done by building, then launching and confirming the window initializes without an SDL/GPU error before the process is terminated (e.g. `go build ... && timeout 6 ./blockcraft.exe`).

`go vet ./...` reports `possible misuse of unsafe.Pointer` on `MapTransferBuffer(uintptr)` calls in `internal/renderer`. These come from the library API (which returns a raw pointer) and match the pattern used by go-sdl3's own examples; the mapping is pinned between `Map`/`Unmap` so the conversions are safe — suppress or ignore them.

## Architecture

The game is a first-person voxel sandbox split into five packages under `internal/` plus an entry point under `cmd/blockcraft`.

- **`app/`** — Owns the SDL3 window, the GPU device, and the main loop. Runs a fixed-timestep physics accumulator (60 Hz) regardless of render rate; renders once per frame capped by vsync. Accumulates mouse motion and keyboard state into a per-tick `player.Input`, drives the player, advances the world, pushes freshly meshed chunks to the GPU, then renders. Handles block break/place via a voxel raycast from the camera.
- **`player/`** — Quaternion first-person camera (yaw around world-up, pitch around local-right, pitch clamped <90° to avoid gimbal lock) plus a swept AABB-vs-voxel body. Collision is resolved per-axis by **snapping flush to the contacted block face** (not reverting), which stops the old reverting approach from leaving the player hovering a fraction above the ground and stair-stepping down each tick. `collides()` shrinks the AABB by a 1e-4 epsilon before `floor` so a flush-touch on an exact integer boundary is *not* reported as a collision — that's what prevents the "can't jump against a wall / teleport into the floor" bugs that a naive check would cause.
- **`renderer/`** — SDL_gpu wrapper. Holds two graphics pipelines: an **opaque base** pipeline (depth write + LESS test) and an **alpha-blended overlay** pipeline (no depth write, LEQUAL test). Draws every chunk's base layer first, then its overlay layer, in one render pass — overlay faces are at the same depth as their base face, so LEQUAL lets them composite. Builds a `4×4 (+ pad)` texture atlas at startup from the PNGs, uploads the view-projection matrix once per frame as a vertex uniform, and does frustum culling per-chunk via `math3d.Frustum`.
- **`world/`** — The voxel store and generation. A `Chunk` is `32³` `BlockID` cells. `World` owns the chunk map and a pool of background goroutines that generate terrain block data off the main thread; meshing runs on the main thread, throttled to a few chunks per frame. Chunks stream in around the player and unload beyond render distance; a chunk is only meshed once its in-range XZ neighbors exist.
- **`math3d/`** — Axis-aligned bounding boxes and Gribb–Hartmann frustum-plane extraction + AABB intersection for chunk culling.

### Assets and threads

The only thread that touches the chunks map or builds mesh data is the **main thread**. Workers generate standalone `Chunk` objects and hand them back through a channel (`World.generated`), so the hot path stays lock-free. GPU uploads also happen on the main thread. Keep it that way — do not mutate `world.World` state from a goroutine.

### Vertex & uniform convention

All chunk geometry is emitted in **world space** so every chunk shares one `view-projection` matrix (pushed once per frame as a vertex uniform at slot 0). Tinting is done on the GPU by multiplying the per-face vertex colour with the sampled texture — the shared `TexturedQuadColor.frag` shader is already `color * texel`, so grayscale block/overlay textures (grass top, side overlay) carry only brightness while the vertex colour carries `face shade × biome tint`. This is how biome tinting works without any new shader.

### Biomes

`BiomeID` + the `Biome` table live in `internal/world/blocks.go`: each biome has a `Surface` block, a `SubFill` block, and a `GrassTint [3]float32` (RGB in 0–1). `World.BiomeAt(wx, wz)` uses low-frequency value noise to pick large contiguous biomes. The mesher (via `World.BiomeSampler()`) calls `GrassTint(biome)` to colour grass top faces and grass side overlays. Grass tiles that must be tinted are flagged in `blocks.go` (`GrassTileTint`) and greyed-out at atlas-build time so their brightness ranges correctly; overlay tiles get their alpha derived from brightness and their RGB forced to white so the tint supplies the final colour. Sand blocks generate in the desert but are never tinted — they always use the colour baked into their own texture.

### Block tinting

Grass tinting is **hardcoded per-face** in `emitBlockFaces` in `internal/world/chunkmesher.go`, not driven by a per-tile flag:

- Top face → `tileGrassTop` grayscale texture × `tintColor(shade, GrassTint(biome))`.
- Side faces → base `tileGrassSide` (un-tinted dirt texture) + `tileGrassSideOverlay` overlay (forced white RGB, alpha from source brightness) × `tintColor(shade, GrassTint(biome))`.
- Bottom → plain dirt.

So a block only needs biome tinting if you add the tinting logic to `emitBlockFaces`. The only per-tile atlas-side data is the `overlayTileNames` map, which `IsOverlayTile()` consults to store the overlay tile with alpha = brightness and RGB = white (so the tint supplies the colour in the vertex shader).

### Adding a biome

Add a `BiomeID` constant and a row in the `biomes` table. The noise threshold in `BiomeAt` decides where it shows up. Grass tinting per biome is automatic from `GrassTint`; no renderer or mesh changes are needed.
