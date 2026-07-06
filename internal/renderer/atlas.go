// Package renderer wraps SDL_gpu into a small voxel renderer: a texture atlas,
// a textured+shaded graphics pipeline and per-chunk GPU meshes with frustum
// culling.
package renderer

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"unsafe"

	"github.com/Zyko0/go-sdl3/sdl"

	"blockcraft-go/assets"
	"blockcraft-go/internal/world"
)

// atlasTilesPerRow is the number of tiles packed into one atlas row. With 16
// block textures this gives a 4x4 layout.
const atlasTilesPerRow = 4

const tilePixelSize = 16

// atlasPad is the number of padding pixels added around every tile in the
// atlas. Each padding row/column duplicates the tile's own edge texel, so any
// sample that bleeds past the tile's content (due to nearest-filtering
// precision at face edges) lands on a copy of the edge texel rather than on
// the neighbouring tile. This is the standard fix for atlas seams.
const atlasPad = 1

// atlasCellSize is the full per-tile footprint in the atlas including padding.
const atlasCellSize = tilePixelSize + 2*atlasPad

// Atlas is the packed block-texture atlas living on the GPU, plus the UV table
// needed by the mesher.
type Atlas struct {
	texture *sdl.GPUTexture
	sampler *sdl.GPUSampler
	size    uint32      // edge length in pixels (square atlas)
	tileUVs [][4]float32 // [tile] = {u0, v0, u1, v1} spanning the content only
}

// NewAtlas loads every block texture named in world.AtlasTileNames, packs them
// (with a 1px duplicated border around each) into a square RGBA atlas and
// uploads it to the GPU.
func NewAtlas(device *sdl.GPUDevice) (*Atlas, error) {
	names := world.AtlasTileNames
	numTiles := len(names)
	rows := (numTiles + atlasTilesPerRow - 1) / atlasTilesPerRow
	atlasW := uint32(atlasTilesPerRow * atlasCellSize)
	atlasH := uint32(rows * atlasCellSize)

	pixels := make([]byte, atlasW*atlasH*4)
	tileUVs := make([][4]float32, numTiles)

	// setPx writes an RGBA pixel into the atlas.
	setPx := func(x, y uint32, r, g, b, a byte) {
		off := int(y*atlasW*4) + int(x*4)
		pixels[off+0] = r
		pixels[off+1] = g
		pixels[off+2] = b
		pixels[off+3] = a
	}

	for i, name := range names {
		data, err := assets.TextureFile(name + ".png")
		if err != nil {
			return nil, errors.New("load texture " + name + ": " + err.Error())
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, errors.New("decode " + name + ": " + err.Error())
		}
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)

		// Overlay tiles are grayscale masks: their brightness encodes the
		// grass-lip shape. We store them as white RGB (so the vertex tint
		// supplies the colour) with alpha = brightness (so dark = transparent).
		// All other tiles are stored opaquely with their original colour.
		overlay := world.IsOverlayTile(uint8(i))

		col := uint32(i % atlasTilesPerRow)
		row := uint32(i / atlasTilesPerRow)
		// Top-left of this tile's content inside the atlas (past the padding).
		ox := col*atlasCellSize + atlasPad
		oy := row*atlasCellSize + atlasPad

		// Helper to read a content texel, clamping indices to [0, tilePixelSize-1]
		// so edge duplication can safely reference the border row/column.
		texel := func(x, y int) (byte, byte, byte, byte) {
			if x < 0 {
				x = 0
			} else if x >= tilePixelSize {
				x = tilePixelSize - 1
			}
			if y < 0 {
				y = 0
			} else if y >= tilePixelSize {
				y = tilePixelSize - 1
			}
			s := rgba.Pix[y*rgba.Stride+x*4:]
			return s[0], s[1], s[2], s[3]
		}

		// Write the content plus the 1px padding border duplicated from the
		// nearest edge texel. ox/oy already point past the padding, so the
		// loop variable (which ranges over [-pad, tile+pad)) is used directly
		// as the offset from them.
		for y := -atlasPad; y < tilePixelSize+atlasPad; y++ {
			for x := -atlasPad; x < tilePixelSize+atlasPad; x++ {
				r, g, b, _ := texel(x, y)
				if overlay {
					// Grayscale mask: brightness → alpha, white RGB.
					brightness := r // r==g==b for grayscale source
					setPx(uint32(int(ox)+x), uint32(int(oy)+y), 255, 255, 255, brightness)
				} else {
					setPx(uint32(int(ox)+x), uint32(int(oy)+y), r, g, b, 255)
				}
			}
		}

		// UV rect spans the full content area (texel 0's left edge to texel 15's
		// right edge) with no inset, so all 16 texels render at full width.
		// Bleeding at the edges is absorbed by the 1px duplicated padding around
		// the tile: a sample that drifts past the content boundary lands on a
		// copy of the edge texel, never on the neighbouring tile.
		u0 := float32(ox) / float32(atlasW)
		v0 := float32(oy) / float32(atlasH)
		u1 := float32(ox+tilePixelSize) / float32(atlasW)
		v1 := float32(oy+tilePixelSize) / float32(atlasH)
		tileUVs[i] = [4]float32{u0, v0, u1, v1}
	}

	texture, err := device.CreateTexture(&sdl.GPUTextureCreateInfo{
		Type:              sdl.GPU_TEXTURETYPE_2D,
		Format:            sdl.GPU_TEXTUREFORMAT_R8G8B8A8_UNORM,
		Width:             atlasW,
		Height:            atlasH,
		LayerCountOrDepth: 1,
		NumLevels:         1,
		SampleCount:       sdl.GPU_SAMPLECOUNT_1,
		Usage:             sdl.GPU_TEXTUREUSAGE_SAMPLER,
	})
	if err != nil {
		return nil, errors.New("create atlas texture: " + err.Error())
	}

	transfer, err := device.CreateTransferBuffer(&sdl.GPUTransferBufferCreateInfo{
		Usage: sdl.GPU_TRANSFERBUFFERUSAGE_UPLOAD,
		Size:  uint32(len(pixels)),
	})
	if err != nil {
		return nil, errors.New("create atlas transfer buffer: " + err.Error())
	}

	ptr, err := device.MapTransferBuffer(transfer, false)
	if err != nil {
		return nil, errors.New("map atlas transfer buffer: " + err.Error())
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(pixels))
	copy(dst, pixels)
	device.UnmapTransferBuffer(transfer)

	cmdbuf, err := device.AcquireCommandBuffer()
	if err != nil {
		return nil, errors.New("acquire atlas cmd buf: " + err.Error())
	}
	copyPass := cmdbuf.BeginCopyPass()
	copyPass.UploadToGPUTexture(
		&sdl.GPUTextureTransferInfo{
			TransferBuffer: transfer,
			Offset:         0,
			PixelsPerRow:   atlasW,
			RowsPerLayer:   atlasH,
		},
		&sdl.GPUTextureRegion{
			Texture: texture,
			W:       atlasW,
			H:       atlasH,
			D:       1,
		},
		false,
	)
	copyPass.End()
	cmdbuf.Submit()
	device.ReleaseTransferBuffer(transfer)

	sampler, err := device.CreateSampler(&sdl.GPUSamplerCreateInfo{
		MinFilter:    sdl.GPU_FILTER_NEAREST,
		MagFilter:    sdl.GPU_FILTER_NEAREST,
		MipmapMode:   sdl.GPU_SAMPLERMIPMAPMODE_NEAREST,
		AddressModeU: sdl.GPU_SAMPLERADDRESSMODE_CLAMP_TO_EDGE,
		AddressModeV: sdl.GPU_SAMPLERADDRESSMODE_CLAMP_TO_EDGE,
		AddressModeW: sdl.GPU_SAMPLERADDRESSMODE_CLAMP_TO_EDGE,
	})
	if err != nil {
		return nil, errors.New("create atlas sampler: " + err.Error())
	}

	return &Atlas{
		texture: texture,
		sampler: sampler,
		size:    atlasW,
		tileUVs: tileUVs,
	}, nil
}

// TileUV returns the inset UV rectangle for a tile, satisfying
// world.AtlasUVProvider so the mesher can resolve UVs without importing SDL.
func (a *Atlas) TileUV(tile uint8) (u0, v0, u1, v1 float32) {
	if int(tile) >= len(a.tileUVs) {
		tile = 0
	}
	uv := a.tileUVs[tile]
	return uv[0], uv[1], uv[2], uv[3]
}

// Destroy releases the GPU resources owned by the atlas.
func (a *Atlas) Destroy(device *sdl.GPUDevice) {
	device.ReleaseTexture(a.texture)
	device.ReleaseSampler(a.sampler)
}
