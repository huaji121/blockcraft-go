package renderer

import (
	"errors"
	"fmt"

	"github.com/Zyko0/go-sdl3/sdl"

	"blockcraft-go/assets"
)

// loadShader loads a precompiled shader, picking the bytecode format that
// matches the device's backend (DXIL for Direct3D, SPIR-V for Vulkan, MSL for
// Metal). All three formats are shipped under assets/shaders/compiled/ so the
// same binary runs on any backend.
func loadShader(device *sdl.GPUDevice, name string, stage sdl.GPUShaderStage,
	numSamplers, numUniformBuffers, numStorageBuffers, numStorageTextures uint32,
) (*sdl.GPUShader, error) {
	formats := device.ShaderFormats()

	var ext, entrypoint string
	switch {
	case formats&sdl.GPU_SHADERFORMAT_SPIRV != 0:
		ext, entrypoint = "spv", "main"
	case formats&sdl.GPU_SHADERFORMAT_DXIL != 0:
		ext, entrypoint = "dxil", "main"
	case formats&sdl.GPU_SHADERFORMAT_MSL != 0:
		ext, entrypoint = "msl", "main0"
	default:
		return nil, errors.New("device supports none of SPIR-V/DXIL/MSL")
	}

	code, err := assets.ShaderCode(name, ext)
	if err != nil {
		return nil, fmt.Errorf("load shader %s.%s: %w", name, ext, err)
	}

	shader, err := device.CreateGPUShader(&sdl.GPUShaderCreateInfo{
		Code:               code,
		Entrypoint:         entrypoint,
		Format:             shaderFormatForExt(ext),
		Stage:              stage,
		NumSamplers:        numSamplers,
		NumUniformBuffers:  numUniformBuffers,
		NumStorageBuffers:  numStorageBuffers,
		NumStorageTextures: numStorageTextures,
	})
	if err != nil {
		return nil, fmt.Errorf("create shader %s: %w", name, err)
	}
	return shader, nil
}

func shaderFormatForExt(ext string) sdl.GPUShaderFormat {
	switch ext {
	case "spv":
		return sdl.GPU_SHADERFORMAT_SPIRV
	case "dxil":
		return sdl.GPU_SHADERFORMAT_DXIL
	case "msl":
		return sdl.GPU_SHADERFORMAT_MSL
	default:
		return sdl.GPU_SHADERFORMAT_INVALID
	}
}
