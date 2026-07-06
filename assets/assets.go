// Package assets embeds the game's static resources (compiled shaders and
// block textures) so the resulting binary is self-contained. The embed
// directives are relative to this file's directory, so the package must live
// alongside the shaders/ and textures/ folders.
package assets

import (
	"embed"
	"errors"
	"fmt"
)

//go:embed all:shaders
var shaders embed.FS

//go:embed all:textures
var textures embed.FS

// ShaderCode returns the precompiled bytecode for the given shader file name
// (e.g. "TexturedQuadColorWithMatrix.vert") in the format supported by device.
// SDL_gpu accepts SPIR-V, DXIL or MSL depending on the backend driver, so we
// ship all three and let the caller pick the matching one.
func ShaderCode(name string, ext string) ([]byte, error) {
	path := fmt.Sprintf("shaders/compiled/%s/%s.%s", ext, name, ext)
	code, err := shaders.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read shader %q: %w", path, err)
	}
	if len(code) == 0 {
		return nil, errors.New("shader " + path + " is empty")
	}
	return code, nil
}

// TextureFile returns the raw bytes of a block texture PNG, e.g. "dirt.png".
func TextureFile(name string) ([]byte, error) {
	return textures.ReadFile("textures/block/" + name)
}
