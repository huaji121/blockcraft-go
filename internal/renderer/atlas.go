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

// atlasTilesPerRow is the number of 16px tiles packed into one atlas row. With
// 16 block textures this gives a 4x4 (64x64px) atlas whose row pitch (256
// bytes) is already 256-byte aligned, so no extra padding is needed for upload.
const atlasTilesPerRow = 4

const tilePixelSize = 16

// Atlas is the packed block-texture atlas living on the GPU, plus the UV table
// needed by the mesher.
type Atlas struct {
	texture    *sdl.GPUTexture
	sampler    *sdl.GPUSampler
	size       uint32 // edge length in pixels (square atlas)
	tileUVs    [][4]float32 // [tile] = {u0, v0, u1, v1} with half-texel inset
}

// NewAtlas loads every block texture named in world.AtlasTileNames, packs them
// into a square RGBA atlas and uploads it to the GPU.
func NewAtlas(device *sdl.GPUDevice) (*Atlas, error) {
	names := world.AtlasTileNames
	numTiles := len(names)
	rows := (numTiles + atlasTilesPerRow - 1) / atlasTilesPerRow
	atlasSize := uint32(atlasTilesPerRow * tilePixelSize) // width
	atlasH := uint32(rows * tilePixelSize)                // height

	pixels := make([]byte, atlasSize*atlasH*4)
	tileUVs := make([][4]float32, numTiles)

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

		col := uint32(i % atlasTilesPerRow)
		row := uint32(i / atlasTilesPerRow)
		// Blit the tile into the atlas, forcing alpha to 255 so every block
		// renders opaquely (the MVP pipeline has no alpha blending).
		for y := 0; y < tilePixelSize; y++ {
			for x := 0; x < tilePixelSize; x++ {
				src := rgba.Pix[(y*rgba.Stride)+x*4 : (y*rgba.Stride)+x*4+4]
				dx := col*tilePixelSize + uint32(x)
				dy := row*tilePixelSize + uint32(y)
				off := int(dy*atlasSize*4) + int(dx*4)
				pixels[off+0] = src[0]
				pixels[off+1] = src[1]
				pixels[off+2] = src[2]
				pixels[off+3] = 255
			}
		}

		// UV rect with a half-texel inset to avoid bleeding between tiles
		// when using nearest filtering at chunk edges.
		inset := 0.5 / float32(atlasSize)
		u0 := float32(col)*tilePixelSize/float32(atlasSize) + inset
		v0 := float32(row)*tilePixelSize/float32(atlasH) + inset
		u1 := float32(col+1)*tilePixelSize/float32(atlasSize) - inset
		v1 := float32(row+1)*tilePixelSize/float32(atlasH) - inset
		tileUVs[i] = [4]float32{u0, v0, u1, v1}
	}

	texture, err := device.CreateTexture(&sdl.GPUTextureCreateInfo{
		Type:              sdl.GPU_TEXTURETYPE_2D,
		Format:            sdl.GPU_TEXTUREFORMAT_R8G8B8A8_UNORM,
		Width:             atlasSize,
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
			PixelsPerRow:   atlasSize,
			RowsPerLayer:   atlasH,
		},
		&sdl.GPUTextureRegion{
			Texture: texture,
			W:       atlasSize,
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
		size:    atlasSize,
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
