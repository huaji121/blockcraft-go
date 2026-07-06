# Blockcraft-go

A 3D voxel sandbox built with [Go](https://go.dev/) and
[go-sdl3](https://github.com/Zyko0/go-sdl3) (SDL3's GPU API). It implements a
first-person controller over a chunked, frustum-culled voxel world with
background chunk generation and ray-cast block placement/removal.

## Build & run

```bash
go build -o blockcraft.exe ./cmd/blockcraft
./blockcraft.exe
```

Requires Go 1.26+. The SDL3 shared library is embedded by `go-sdl3/bin/binsdl`,
so no system SDL3 install is needed.

## Controls

| Action          | Input                          |
| --------------- | ------------------------------ |
| Move            | `W` `A` `S` `D`                |
| Look            | Mouse (pointer is locked)      |
| Jump            | `Space`                        |
| Sprint          | `Ctrl`                         |
| Crouch / fly down | `Shift`                      |
| Toggle fly mode | `F`                            |
| Remove block    | Left click                     |
| Place stone     | Right click                    |
| Release cursor  | `Esc` (click the window to resume) |
| Quit            | Close the window               |

## Architecture

```
cmd/blockcraft/      entry point
internal/
  app/               game loop, SDL/window init, input, fixed-timestep physics
  player/            quaternion first-person camera + AABB voxel collision
  renderer/          SDL_gpu pipeline, texture atlas, per-chunk GPU meshes
  world/             chunk store, terrain gen, mesher, raycast, world manager
  math3d/            AABB + frustum extraction/culling
assets/
  shaders/           HLSL sources + precompiled DXIL/SPIR-V/MSL bytecode
  textures/block/    block textures packed into an atlas at runtime
```

### Chunk system

- A `Chunk` is `32×32×32` `BlockID` cells (`internal/world/chunk.go`).
- `World` (`internal/world/world.go`) owns the chunk map and a pool of
  background goroutines that generate terrain block data off the main thread.
  Meshing runs on the main thread but is throttled to a few chunks per frame so
  frame time stays bounded while a new area streams in.
- Chunks load dynamically around the player and unload beyond the render
  distance (default 5 chunks). A chunk is meshed only once its in-range XZ
  neighbours exist, which avoids stale border faces.
- Each chunk mesh is built with face culling: a face is emitted only when the
  neighbour cell is air or a different transparent type, so internal faces are
  skipped (`internal/world/chunkmesher.go`).
- Frustum culling (`internal/math3d/frustum.go`) skips chunks whose AABB is
  outside the camera's view, extracted from the view-projection matrix.

### Camera

The player orientation is a quaternion composed from yaw (world up) and pitch
(local right), with pitch clamped just under 90°. Composing rotations as
quaternions and clamping pitch avoids the gimbal-lock singularity of an Euler
YXZ sequence at the poles.

### Rendering

- One graphics pipeline: textured + per-vertex shaded blocks with depth testing
  and back-face culling. Vertex data is emitted in world space so a single
  view-projection uniform is shared by every chunk draw.
- A 4×4 (64×64 px) texture atlas is built at startup from the PNGs in
  `assets/textures/block/`; the mesher resolves each face's UVs through the
  atlas with a half-texel inset to prevent bleeding.
- Shaders are shipped precompiled in all three backends (DXIL/SPIR-V/MSL); the
  renderer picks the format matching the SDL_gpu driver at runtime.

## Notes

- `go vet` reports `possible misuse of unsafe.Pointer` in the renderer. These
  come from `MapTransferBuffer` returning a `uintptr` (the library's API) and
  match the pattern used by go-sdl3's own examples; the mapping is pinned
  between `Map`/`Unmap` so the conversions are safe.
