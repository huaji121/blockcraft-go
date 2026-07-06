package renderer

import (
	"errors"
	"unsafe"

	"github.com/Zyko0/go-sdl3/sdl"
	"github.com/go-gl/mathgl/mgl32"

	"blockcraft-go/internal/math3d"
	"blockcraft-go/internal/world"
)

// skyBlue is the clear colour used for the background.
var skyBlue = sdl.FColor{R: 0.529, G: 0.808, B: 0.922, A: 1.0}

// vertexShaderName / fragmentShaderName identify the precompiled shaders used
// for every block face. See assets/shaders/source for the HLSL.
const (
	vertexShaderName   = "TexturedQuadColorWithMatrix.vert"
	fragmentShaderName = "TexturedQuadColor.frag"
)

// ChunkMesh is the GPU-side geometry for one chunk.
type ChunkMesh struct {
	vertexBuffer *sdl.GPUBuffer
	indexBuffer  *sdl.GPUBuffer
	indexCount   uint32
}

// Renderer owns the GPU pipeline, atlas, depth target and the set of uploaded
// chunk meshes. It is single-threaded (driven from the main loop).
type Renderer struct {
	device *sdl.GPUDevice
	window *sdl.Window

	pipeline    *sdl.GPUGraphicsPipeline
	atlas       *Atlas
	depthTexture *sdl.GPUTexture
	depthW      uint32
	depthH      uint32

	meshes map[[3]int32]*ChunkMesh
}

// NewRenderer builds the atlas, shaders, pipeline and initial depth target.
func NewRenderer(device *sdl.GPUDevice, window *sdl.Window) (*Renderer, error) {
	r := &Renderer{
		device: device,
		window: window,
		meshes: make(map[[3]int32]*ChunkMesh),
	}

	atlas, err := NewAtlas(device)
	if err != nil {
		return nil, err
	}
	r.atlas = atlas

	if err := r.initPipeline(); err != nil {
		return nil, err
	}

	if err := r.ensureDepthTexture(); err != nil {
		return nil, err
	}

	return r, nil
}

// initPipeline creates the graphics pipeline used to draw block faces.
func (r *Renderer) initPipeline() error {
	vert, err := loadShader(r.device, vertexShaderName, sdl.GPU_SHADERSTAGE_VERTEX, 0, 1, 0, 0)
	if err != nil {
		return err
	}
	frag, err := loadShader(r.device, fragmentShaderName, sdl.GPU_SHADERSTAGE_FRAGMENT, 1, 0, 0, 0)
	if err != nil {
		r.device.ReleaseShader(vert)
		return err
	}

	colorTargets := []sdl.GPUColorTargetDescription{
		{Format: r.device.SwapchainTextureFormat(r.window)},
	}

	vertexBufferDescs := []sdl.GPUVertexBufferDescription{{
		Slot:      0,
		InputRate: sdl.GPU_VERTEXINPUTRATE_VERTEX,
		Pitch:     uint32(unsafe.Sizeof(world.VoxelVertex{})),
	}}

	vertexAttrs := []sdl.GPUVertexAttribute{
		{BufferSlot: 0, Format: sdl.GPU_VERTEXELEMENTFORMAT_FLOAT3, Location: 0, Offset: 0},
		{BufferSlot: 0, Format: sdl.GPU_VERTEXELEMENTFORMAT_FLOAT2, Location: 1, Offset: 12},
		{BufferSlot: 0, Format: sdl.GPU_VERTEXELEMENTFORMAT_UBYTE4_NORM, Location: 2, Offset: 20},
	}

	pipeline, err := r.device.CreateGraphicsPipeline(&sdl.GPUGraphicsPipelineCreateInfo{
		TargetInfo: sdl.GPUGraphicsPipelineTargetInfo{
			ColorTargetDescriptions: colorTargets,
			HasDepthStencilTarget:   true,
			DepthStencilFormat:      sdl.GPU_TEXTUREFORMAT_D16_UNORM,
		},
		DepthStencilState: sdl.GPUDepthStencilState{
			EnableDepthTest:  true,
			EnableDepthWrite: true,
			CompareOp:        sdl.GPU_COMPAREOP_LESS,
		},
		RasterizerState: sdl.GPURasterizerState{
			CullMode:  sdl.GPU_CULLMODE_BACK,
			FillMode:  sdl.GPU_FILLMODE_FILL,
			FrontFace: sdl.GPU_FRONTFACE_COUNTER_CLOCKWISE,
		},
		VertexInputState: sdl.GPUVertexInputState{
			VertexBufferDescriptions: vertexBufferDescs,
			VertexAttributes:         vertexAttrs,
		},
		PrimitiveType:  sdl.GPU_PRIMITIVETYPE_TRIANGLELIST,
		VertexShader:   vert,
		FragmentShader: frag,
	})
	r.device.ReleaseShader(vert)
	r.device.ReleaseShader(frag)
	if err != nil {
		return errors.New("create pipeline: " + err.Error())
	}
	r.pipeline = pipeline
	return nil
}

// ensureDepthTexture (re)creates the depth target to match the window's pixel
// size. Called at init and whenever the swapchain size changes.
func (r *Renderer) ensureDepthTexture() error {
	w, h, err := r.window.SizeInPixels()
	if err != nil {
		return errors.New("get window pixel size: " + err.Error())
	}
	wu, hu := uint32(w), uint32(h)
	if r.depthTexture != nil && wu == r.depthW && hu == r.depthH {
		return nil
	}
	if r.depthTexture != nil {
		r.device.ReleaseTexture(r.depthTexture)
		r.depthTexture = nil
	}
	tex, err := r.device.CreateTexture(&sdl.GPUTextureCreateInfo{
		Type:              sdl.GPU_TEXTURETYPE_2D,
		Format:            sdl.GPU_TEXTUREFORMAT_D16_UNORM,
		Width:             wu,
		Height:            hu,
		LayerCountOrDepth: 1,
		NumLevels:         1,
		SampleCount:       sdl.GPU_SAMPLECOUNT_1,
		Usage:             sdl.GPU_TEXTUREUSAGE_DEPTH_STENCIL_TARGET,
	})
	if err != nil {
		return errors.New("create depth texture: " + err.Error())
	}
	r.depthTexture = tex
	r.depthW = wu
	r.depthH = hu
	return nil
}

// Atlas returns the atlas as a world.AtlasUVProvider so the world/mesher can
// resolve tile UVs without importing the renderer package.
func (r *Renderer) Atlas() world.AtlasUVProvider { return r.atlas }

// ApplyWorldEvents drains pending mesh uploads and unloads from the world and
// keeps the GPU mesh set in sync. Call once per frame before Render.
func (r *Renderer) ApplyWorldEvents(w *world.World) {
	for _, pos := range w.DrainUnloads() {
		r.releaseMesh(pos)
	}
	for _, up := range w.DrainUploads() {
		r.uploadMesh(up)
	}
}

// releaseMesh frees the GPU buffers for a chunk, if any.
func (r *Renderer) releaseMesh(pos world.ChunkPos) {
	m, ok := r.meshes[pos.Key()]
	if !ok {
		return
	}
	if m.vertexBuffer != nil {
		r.device.ReleaseBuffer(m.vertexBuffer)
	}
	if m.indexBuffer != nil {
		r.device.ReleaseBuffer(m.indexBuffer)
	}
	delete(r.meshes, pos.Key())
}

// uploadMesh (re)creates the GPU mesh for a chunk from CPU vertex/index data.
// An empty mesh frees any previous buffers and stores nothing.
func (r *Renderer) uploadMesh(up world.MeshUpload) {
	r.releaseMesh(up.Pos)
	if len(up.Data.Vertices) == 0 || len(up.Data.Indices) == 0 {
		return
	}

	vbuf, ibuf, err := r.uploadChunkGeometry(up.Data)
	if err != nil {
		// Skip this chunk rather than tearing down the whole renderer; the
		// world will re-queue it as dirty if needed.
		return
	}
	r.meshes[up.Pos.Key()] = &ChunkMesh{
		vertexBuffer: vbuf,
		indexBuffer:  ibuf,
		indexCount:   uint32(len(up.Data.Indices)),
	}
}

// uploadChunkGeometry creates and fills the vertex/index buffers for one mesh.
func (r *Renderer) uploadChunkGeometry(data world.ChunkMeshData) (*sdl.GPUBuffer, *sdl.GPUBuffer, error) {
	vertSize := uint32(len(data.Vertices)) * uint32(unsafe.Sizeof(world.VoxelVertex{}))
	indexSize := uint32(len(data.Indices)) * uint32(unsafe.Sizeof(uint32(0)))

	vbuf, err := r.device.CreateBuffer(&sdl.GPUBufferCreateInfo{
		Usage: sdl.GPU_BUFFERUSAGE_VERTEX,
		Size:  vertSize,
	})
	if err != nil {
		return nil, nil, errors.New("create vertex buffer: " + err.Error())
	}
	ibuf, err := r.device.CreateBuffer(&sdl.GPUBufferCreateInfo{
		Usage: sdl.GPU_BUFFERUSAGE_INDEX,
		Size:  indexSize,
	})
	if err != nil {
		r.device.ReleaseBuffer(vbuf)
		return nil, nil, errors.New("create index buffer: " + err.Error())
	}

	transfer, err := r.device.CreateTransferBuffer(&sdl.GPUTransferBufferCreateInfo{
		Usage: sdl.GPU_TRANSFERBUFFERUSAGE_UPLOAD,
		Size:  vertSize + indexSize,
	})
	if err != nil {
		r.device.ReleaseBuffer(vbuf)
		r.device.ReleaseBuffer(ibuf)
		return nil, nil, errors.New("create transfer buffer: " + err.Error())
	}

	ptr, err := r.device.MapTransferBuffer(transfer, false)
	if err != nil {
		r.device.ReleaseBuffer(vbuf)
		r.device.ReleaseBuffer(ibuf)
		r.device.ReleaseTransferBuffer(transfer)
		return nil, nil, errors.New("map transfer buffer: " + err.Error())
	}

	// Vertex data goes at the start, index data immediately after.
	vertDst := unsafe.Slice((*world.VoxelVertex)(unsafe.Pointer(ptr)), len(data.Vertices))
	copy(vertDst, data.Vertices)
	idxDst := unsafe.Slice((*uint32)(unsafe.Pointer(ptr+uintptr(vertSize))), len(data.Indices))
	copy(idxDst, data.Indices)
	r.device.UnmapTransferBuffer(transfer)

	cmdbuf, err := r.device.AcquireCommandBuffer()
	if err != nil {
		r.device.ReleaseBuffer(vbuf)
		r.device.ReleaseBuffer(ibuf)
		r.device.ReleaseTransferBuffer(transfer)
		return nil, nil, errors.New("acquire cmd buf: " + err.Error())
	}
	cp := cmdbuf.BeginCopyPass()
	cp.UploadToGPUBuffer(
		&sdl.GPUTransferBufferLocation{TransferBuffer: transfer, Offset: 0},
		&sdl.GPUBufferRegion{Buffer: vbuf, Offset: 0, Size: vertSize},
		false,
	)
	cp.UploadToGPUBuffer(
		&sdl.GPUTransferBufferLocation{TransferBuffer: transfer, Offset: vertSize},
		&sdl.GPUBufferRegion{Buffer: ibuf, Offset: 0, Size: indexSize},
		false,
	)
	cp.End()
	cmdbuf.Submit()
	r.device.ReleaseTransferBuffer(transfer)

	return vbuf, ibuf, nil
}

// Render draws all visible chunk meshes with frustum culling. viewproj is the
// combined view-projection matrix pushed as the vertex uniform.
func (r *Renderer) Render(viewproj mgl32.Mat4) error {
	if err := r.ensureDepthTexture(); err != nil {
		return err
	}

	cmdbuf, err := r.device.AcquireCommandBuffer()
	if err != nil {
		return errors.New("acquire cmd buf: " + err.Error())
	}

	swapchain, err := cmdbuf.WaitAndAcquireGPUSwapchainTexture(r.window)
	if err != nil {
		cmdbuf.Cancel()
		return errors.New("acquire swapchain: " + err.Error())
	}
	if swapchain == nil || swapchain.Texture == nil {
		// Window minimised / no drawable yet; nothing to render.
		cmdbuf.Cancel()
		return nil
	}

	// Recreate the depth target if the swapchain size changed (window resize).
	if swapchain.Width != r.depthW || swapchain.Height != r.depthH {
		cmdbuf.Cancel()
		if err := r.recreateDepth(swapchain.Width, swapchain.Height); err != nil {
			return err
		}
		return nil // retry next frame
	}

	frustum := math3d.FrustumFromMatrix(viewproj)

	// View-projection uniform bytes, pushed per-draw below. SDL_gpu's push
	// uniform model provides the most-recently-pushed data to each draw, so we
	// re-push the (constant) matrix before every chunk to be safe.
	vpBytes := unsafe.Slice((*byte)(unsafe.Pointer(&viewproj[0])), unsafe.Sizeof(viewproj))

	colorTarget := sdl.GPUColorTargetInfo{
		Texture:    swapchain.Texture,
		ClearColor: skyBlue,
		LoadOp:     sdl.GPU_LOADOP_CLEAR,
		StoreOp:    sdl.GPU_STOREOP_STORE,
	}
	depthTarget := sdl.GPUDepthStencilTargetInfo{
		Texture:        r.depthTexture,
		ClearDepth:     1,
		LoadOp:         sdl.GPU_LOADOP_CLEAR,
		StoreOp:        sdl.GPU_STOREOP_STORE,
		StencilLoadOp:  sdl.GPU_LOADOP_CLEAR,
		StencilStoreOp: sdl.GPU_STOREOP_STORE,
		Cycle:          true,
	}

	pass := cmdbuf.BeginRenderPass([]sdl.GPUColorTargetInfo{colorTarget}, &depthTarget)
	pass.BindGraphicsPipeline(r.pipeline)
	pass.BindFragmentSamplers([]sdl.GPUTextureSamplerBinding{
		{Texture: r.atlas.texture, Sampler: r.atlas.sampler},
	})

	drawn, culled := 0, 0
	for key, mesh := range r.meshes {
		pos := world.ChunkPos{X: key[0], Y: key[1], Z: key[2]}
		minX, minY, minZ, maxX, maxY, maxZ := world.ChunkAABB(pos)
		box := math3d.AABB{
			Min: mgl32.Vec3{minX, minY, minZ},
			Max: mgl32.Vec3{maxX, maxY, maxZ},
		}
		if !frustum.Intersects(box) {
			culled++
			continue
		}
		cmdbuf.PushVertexUniformData(0, vpBytes)
		pass.BindVertexBuffers([]sdl.GPUBufferBinding{{Buffer: mesh.vertexBuffer, Offset: 0}})
		pass.BindIndexBuffer(&sdl.GPUBufferBinding{Buffer: mesh.indexBuffer, Offset: 0}, sdl.GPU_INDEXELEMENTSIZE_32BIT)
		pass.DrawIndexedPrimitives(mesh.indexCount, 1, 0, 0, 0)
		drawn++
	}
	pass.End()

	cmdbuf.Submit()
	_ = drawn
	_ = culled
	return nil
}

// recreateDepth rebuilds the depth target at a new size.
func (r *Renderer) recreateDepth(w, h uint32) error {
	if r.depthTexture != nil {
		r.device.ReleaseTexture(r.depthTexture)
		r.depthTexture = nil
	}
	tex, err := r.device.CreateTexture(&sdl.GPUTextureCreateInfo{
		Type:              sdl.GPU_TEXTURETYPE_2D,
		Format:            sdl.GPU_TEXTUREFORMAT_D16_UNORM,
		Width:             w,
		Height:            h,
		LayerCountOrDepth: 1,
		NumLevels:         1,
		SampleCount:       sdl.GPU_SAMPLECOUNT_1,
		Usage:             sdl.GPU_TEXTUREUSAGE_DEPTH_STENCIL_TARGET,
	})
	if err != nil {
		return errors.New("recreate depth texture: " + err.Error())
	}
	r.depthTexture = tex
	r.depthW = w
	r.depthH = h
	return nil
}

// Destroy releases all GPU resources owned by the renderer.
func (r *Renderer) Destroy() {
	for key := range r.meshes {
		m := r.meshes[key]
		if m.vertexBuffer != nil {
			r.device.ReleaseBuffer(m.vertexBuffer)
		}
		if m.indexBuffer != nil {
			r.device.ReleaseBuffer(m.indexBuffer)
		}
		delete(r.meshes, key)
	}
	if r.pipeline != nil {
		r.device.ReleaseGraphicsPipeline(r.pipeline)
	}
	if r.atlas != nil {
		r.atlas.Destroy(r.device)
	}
	if r.depthTexture != nil {
		r.device.ReleaseTexture(r.depthTexture)
	}
}
