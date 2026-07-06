// Package world implements the voxel chunk store, procedural generation, mesh
// extraction and voxel raycasting used by the renderer and the player.
package world

// BlockID identifies a block type. 0 is reserved for air (empty cell).
type BlockID uint8

const (
	Air BlockID = iota
	Dirt
	Grass
	Stone
	Cobblestone
	Bedrock
	OakLog
	OakPlanks
	Sand
	Gravel
	CoalOre
	IronOre
	DiamondOre
	GoldOre
	Glass
	NumBlockTypes
)

// Face direction constants. The ordering is fixed and shared by the mesher and
// the raycast "pick adjacent face" logic, so the index produced by a ray hit
// maps directly to the neighbour offset used for block placement.
const (
	FacePosX = iota // +X / east
	FaceNegX        // -X / west
	FacePosY        // +Y / top
	FaceNegY        // -Y / bottom
	FacePosZ        // +Z / south
	FaceNegZ        // -Z / north
	NumFaces
)

// faceNormal returns the unit normal (and the neighbour offset) for a face.
var faceOffsets = [NumFaces][3]int32{
	{1, 0, 0},  // +X
	{-1, 0, 0}, // -X
	{0, 1, 0},  // +Y
	{0, -1, 0}, // -Y
	{0, 0, 1},  // +Z
	{0, 0, -1}, // -Z
}

// blockDef describes how a block type looks and behaves.
type blockDef struct {
	name         string
	tiles        [NumFaces]uint8 // atlas tile index per face
	transparent  bool            // faces always drawn even against same type (glass, leaves)
}

// AtlasTileNames lists the block textures packed into the texture atlas, in
// tile order. The renderer builds the atlas from this list, so a tile's index
// here is its index in the atlas. Must stay in sync with the tiles[] entries.
var AtlasTileNames = []string{
	"dirt",                    // 0
	"grass_block_top",         // 1  (grayscale — tinted by biome)
	"grass_block_side",        // 2  (base layer, not tinted)
	"stone",                   // 3
	"cobblestone",             // 4
	"bedrock",                 // 5
	"oak_log_top",             // 6
	"oak_log",                 // 7
	"oak_planks",              // 8
	"sand",                    // 9
	"gravel",                  // 10
	"coal_ore",                // 11
	"iron_ore",                // 12
	"diamond_ore",             // 13
	"gold_ore",                // 14
	"glass",                   // 15
	"grass_block_side_overlay", // 16 (overlay layer — tinted by biome, alpha from brightness)
}

// Convenience tile indices.
const (
	tileDirt             = 0
	tileGrassTop         = 1
	tileGrassSide        = 2
	tileStone            = 3
	tileCobblestone      = 4
	tileBedrock          = 5
	tileOakLogTop        = 6
	tileOakLog           = 7
	tileOakPlanks        = 8
	tileSand             = 9
	tileGravel           = 10
	tileGlass            = 15
	tileGrassSideOverlay = 16
)

// OverlayTileNames is the set of atlas tiles whose alpha is derived from the
// source texture's brightness (rather than forced opaque). These are the
// tinted overlay layers composited over a base layer with alpha blending.
var overlayTileNames = map[string]bool{
	"grass_block_side_overlay": true,
}

// IsOverlayTile reports whether the tile at the given atlas index is an
// alpha-derived overlay tile (used by the renderer when packing the atlas).
func IsOverlayTile(tile uint8) bool {
	if int(tile) >= len(AtlasTileNames) {
		return false
	}
	return overlayTileNames[AtlasTileNames[tile]]
}

var blockDefs = [NumBlockTypes]blockDef{
	Air:         {name: "air"},
	Dirt:        {name: "dirt", tiles: allFaces(tileDirt)},
	Grass:       {name: "grass", tiles: sideTopBottom(tileGrassSide, tileGrassTop, tileDirt)},
	Stone:       {name: "stone", tiles: allFaces(tileStone)},
	Cobblestone: {name: "cobblestone", tiles: allFaces(tileCobblestone)},
	Bedrock:     {name: "bedrock", tiles: allFaces(tileBedrock)},
	OakLog:      {name: "oak_log", tiles: sideTopBottom(tileOakLog, tileOakLogTop, tileOakLogTop)},
	OakPlanks:   {name: "oak_planks", tiles: allFaces(tileOakPlanks)},
	Sand:        {name: "sand", tiles: allFaces(tileSand)},
	Gravel:      {name: "gravel", tiles: allFaces(tileGravel)},
	CoalOre:     {name: "coal_ore", tiles: allFaces(tileStone)}, // reuses stone tile; real ore texture differs
	IronOre:     {name: "iron_ore", tiles: allFaces(tileStone)},
	DiamondOre:  {name: "diamond_ore", tiles: allFaces(tileStone)},
	GoldOre:     {name: "gold_ore", tiles: allFaces(tileStone)},
	Glass:       {name: "glass", tiles: allFaces(tileGlass), transparent: true},
}

// allFaces returns a tile mapping where every face uses the same tile.
func allFaces(tile uint8) [NumFaces]uint8 {
	return [NumFaces]uint8{tile, tile, tile, tile, tile, tile}
}

// sideTopBottom returns a mapping where the four horizontal faces share a tile,
// the top uses topTile and the bottom uses bottomTile. The face ordering is
// (+X, -X, +Y/top, -Y/bottom, +Z, -Z).
func sideTopBottom(side, top, bottom uint8) [NumFaces]uint8 {
	return [NumFaces]uint8{side, side, top, bottom, side, side}
}

// IsAir reports whether the block is empty.
func (b BlockID) IsAir() bool { return b == Air }

// IsOpaque reports whether the block fully occludes the face of an adjacent
// block. Transparent blocks (glass) and air do not.
func (b BlockID) IsOpaque() bool {
	if b == Air {
		return false
	}
	return !blockDefs[b].transparent
}

// Tile returns the atlas tile index for the given face of this block.
func (b BlockID) Tile(face int) uint8 { return blockDefs[b].tiles[face] }

// Name returns the human-readable block name.
func (b BlockID) Name() string { return blockDefs[b].name }

// BiomeID identifies a biome. Biomes are determined per world (x,z) column and
// control terrain surface blocks and grass tint colour.
type BiomeID uint8

const (
	BiomePlains BiomeID = iota
	BiomeDesert
	NumBiomes
)

// Biome describes a biome's terrain and appearance.
type Biome struct {
	ID        BiomeID
	Name      string
	Surface   BlockID      // block placed at the terrain surface
	SubFill   BlockID      // block placed a few layers below the surface
	GrassTint [3]float32   // RGB in [0,1] used to tint grass top + side overlay
}

// biomes is the registered biome table, indexed by BiomeID. New biomes are
// added by appending a const above and an entry here — the rest of the engine
// picks them up automatically.
var biomes = [NumBiomes]Biome{
	BiomePlains: {
		ID:        BiomePlains,
		Name:      "plains",
		Surface:   Grass,
		SubFill:   Dirt,
		GrassTint: [3]float32{0.42, 0.70, 0.30}, // lush green
	},
	BiomeDesert: {
		ID:        BiomeDesert,
		Name:      "desert",
		Surface:   Sand,
		SubFill:   Sand,
		GrassTint: [3]float32{0.768, 0.639, 0.329}, // #c4a354 withered yellow
	},
}

// BiomeByID returns the biome definition for the given ID.
func BiomeByID(id BiomeID) Biome { return biomes[id] }

// GrassTint returns the grass tint colour (RGB in [0,1]) for the biome at the
// given world column. This is the single source of truth used by the mesher to
// tint grass-block top faces and side overlays.
func GrassTint(id BiomeID) [3]float32 { return biomes[id].GrassTint }
