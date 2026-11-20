/*
Copyright (c) 2026 Lauris Bukšis <lauris@nix.lv>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package tiled

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The infinite.tmx fixture is laid out as follows (global coordinates):
//
//	layer "Floor" (csv):
//	  chunk (-16,-16) 16x16: entirely empty
//	  chunk (0,0)     16x16: gids 1..12 in the top-left 4x3 area, rest empty
//	  chunk (16,0)    4x2:   flip-flag probe, see TestInfiniteGIDFlipFlags
//	layer "Base64 GZIP" (base64+gzip):
//	  chunk (8,-8)  4x2: gids 21..24 then four zeros
//	  chunk (-8,8)  2x2: gids 25..28
func loadInfiniteFixture(t *testing.T) *Map {
	t.Helper()
	m, err := LoadFile(filepath.Join(GetAssetsDirectory(), "infinite.tmx"))
	require.NoError(t, err)
	require.NotNil(t, m)
	return m
}

// tilesetTileID returns the tile's tileset-local ID for a non-empty tile.
func tilesetTileID(t *testing.T, tile *LayerTile) uint32 {
	t.Helper()
	require.NotNil(t, tile)
	require.False(t, tile.Nil)
	require.NotNil(t, tile.Tileset)
	return tile.ID
}

func TestInfiniteMapLoad(t *testing.T) {
	m := loadInfiniteFixture(t)

	assert.True(t, m.Infinite)
	assert.Len(t, m.Layers, 2)

	floor := m.Layers[0]
	assert.Equal(t, "Floor", floor.Name)
	assert.Nil(t, floor.Tiles, "infinite layers must not expose a fixed-size Tiles slice")
	assert.Len(t, floor.Chunks, 3)
	assert.False(t, floor.IsEmpty())

	// Chunks are exposed in sorted (Y, X) order regardless of document order.
	assert.Equal(t, [][4]int{
		{-16, -16, 16, 16},
		{0, 0, 16, 16},
		{16, 0, 4, 2},
	}, [][4]int{
		{floor.Chunks[0].X, floor.Chunks[0].Y, floor.Chunks[0].Width, floor.Chunks[0].Height},
		{floor.Chunks[1].X, floor.Chunks[1].Y, floor.Chunks[1].Width, floor.Chunks[1].Height},
		{floor.Chunks[2].X, floor.Chunks[2].Y, floor.Chunks[2].Width, floor.Chunks[2].Height},
	})
	for _, c := range floor.Chunks {
		assert.Len(t, c.Tiles, c.Width*c.Height)
	}

	assert.True(t, floor.Chunks[0].IsEmpty(), "all-zero chunk should be empty")
	assert.False(t, floor.Chunks[1].IsEmpty())

	// Negative-coordinate chunk: TileAt and Chunk.TileAt agree.
	assert.True(t, floor.Chunks[0].TileAt(-1, -1).Nil)
	assert.True(t, floor.TileAt(-1, -1).Nil)
	assert.True(t, floor.Chunks[0].Contains(-16, -16))
	assert.False(t, floor.Chunks[0].Contains(0, -16))

	// Index-based helpers have no meaning for chunked layers; they must not
	// divide by the (zero) map width.
	x, y := floor.GetTilePosition(5)
	assert.Equal(t, 0, x)
	assert.Equal(t, 0, y)
}

func TestInfiniteTileAtCSV(t *testing.T) {
	m := loadInfiniteFixture(t)
	floor := m.Layers[0]

	tests := []struct {
		x, y int
		gid  uint32 // 0 means empty
	}{
		{0, 0, 1}, {1, 0, 2}, {2, 0, 3}, {3, 0, 4}, {4, 0, 0},
		{0, 1, 5}, {1, 1, 6}, {2, 1, 7}, {3, 1, 8},
		{0, 2, 9}, {1, 2, 10}, {2, 2, 11}, {3, 2, 12}, {4, 2, 0},
		{15, 15, 0},   // inside the chunk, empty cell
		{-1, -1, 0},   // inside the all-empty negative chunk
		{-16, -16, 0}, // first cell of the negative chunk
		{-17, 0, 0},   // before any chunk
		{0, -17, 0},   // above any chunk
		{1000, 1000, 0},
		{-1000, 1000, 0},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d,%d", tc.x, tc.y), func(t *testing.T) {
			tile := floor.TileAt(tc.x, tc.y)
			require.NotNil(t, tile)
			if tc.gid == 0 {
				assert.True(t, tile.IsNil(), "expected empty tile at (%d,%d)", tc.x, tc.y)
				return
			}
			// The fixture's only tileset has FirstGID 1, so ID == GID-1.
			assert.Equal(t, tc.gid-1, tilesetTileID(t, tile), "at (%d,%d)", tc.x, tc.y)
		})
	}
}

func TestInfiniteChunkLocalTileAt(t *testing.T) {
	m := loadInfiniteFixture(t)
	floor := m.Layers[0]
	chunk := floor.Chunks[1] // (0,0) 16x16

	assert.Equal(t, uint32(0), tilesetTileID(t, chunk.TileAt(0, 0)))
	assert.Equal(t, uint32(11), tilesetTileID(t, chunk.TileAt(3, 2)))
	assert.True(t, chunk.TileAt(16, 0).Nil, "outside the chunk")
	assert.True(t, chunk.TileAt(-1, 0).Nil, "outside the chunk")
}

// TestInfiniteGIDFlipFlags verifies the four flip/rotation bits survive
// chunk decoding, including the hex 120-degree rotation bit.
func TestInfiniteGIDFlipFlags(t *testing.T) {
	m := loadInfiniteFixture(t)
	floor := m.Layers[0]

	// Row 0 of the (16,0) chunk: gids 0x80000001, 0x40000002, 0x20000003, 0x10000004.
	h := floor.TileAt(16, 0)
	require.False(t, h.IsNil())
	assert.True(t, h.HorizontalFlip)
	assert.False(t, h.VerticalFlip)
	assert.False(t, h.DiagonalFlip)
	assert.False(t, h.RotatedHexagonal120)
	assert.Equal(t, uint32(0), h.ID)

	v := floor.TileAt(17, 0)
	require.False(t, v.IsNil())
	assert.False(t, v.HorizontalFlip)
	assert.True(t, v.VerticalFlip)
	assert.Equal(t, uint32(1), v.ID)

	d := floor.TileAt(18, 0)
	require.False(t, d.IsNil())
	assert.True(t, d.DiagonalFlip)
	assert.Equal(t, uint32(2), d.ID)

	hex := floor.TileAt(19, 0)
	require.False(t, hex.IsNil())
	assert.True(t, hex.RotatedHexagonal120)
	assert.Equal(t, uint32(3), hex.ID)

	// Second row has no flags.
	plain := floor.TileAt(16, 1)
	require.False(t, plain.IsNil())
	assert.False(t, plain.HorizontalFlip || plain.VerticalFlip || plain.DiagonalFlip || plain.RotatedHexagonal120)
	assert.Equal(t, uint32(0), plain.ID)
}

func TestInfiniteBase64Gzip(t *testing.T) {
	m := loadInfiniteFixture(t)
	b64 := m.Layers[1]
	require.Equal(t, "Base64 GZIP", b64.Name)
	require.Nil(t, b64.Tiles)
	assert.Len(t, b64.Chunks, 2)

	// chunk (8,-8): gids 21..24 then 4 zeros, 4x2.
	// FirstGID is 1, so ID == GID-1.
	expected := map[[2]int]uint32{
		{8, -8}: 20, {9, -8}: 21, {10, -8}: 22, {11, -8}: 23,
		{8, -7}: 0, {9, -7}: 0, {10, -7}: 0, {11, -7}: 0,
	}
	for cell, id := range expected {
		tile := b64.TileAt(cell[0], cell[1])
		if id == 0 && cell[1] == -7 {
			assert.True(t, tile.IsNil(), "cell %v", cell)
			continue
		}
		// Empty cells in the first row would be id 0 too; distinguish by GID.
		if tile.IsNil() {
			assert.Equal(t, uint32(0), id, "unexpected empty cell %v", cell)
			continue
		}
		assert.Equal(t, id, tilesetTileID(t, tile), "cell %v", cell)
	}

	// chunk (-8,8): gids 25..28 in a 2x2.
	assert.Equal(t, uint32(24), tilesetTileID(t, b64.TileAt(-8, 8)))
	assert.Equal(t, uint32(25), tilesetTileID(t, b64.TileAt(-7, 8)))
	assert.Equal(t, uint32(26), tilesetTileID(t, b64.TileAt(-8, 9)))
	assert.Equal(t, uint32(27), tilesetTileID(t, b64.TileAt(-7, 9)))

	// Sparse emptiness between the two chunks.
	assert.True(t, b64.TileAt(0, 0).Nil)
	assert.True(t, b64.TileAt(8, 8).Nil)
}

// TestInfiniteXMLEncoding covers the per-tile XML encoding for chunks, which
// the fixture does not use (kept inline because it needs many elements).
func TestInfiniteXMLEncoding(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="XML" width="0" height="0">
  <data>
   <chunk x="-2" y="-2" width="2" height="2"><tile gid="1"/><tile gid="0"/><tile gid="3"/><tile gid="4"/></chunk>
  </data>
 </layer>
</map>`

	m, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	require.NoError(t, err)
	require.Len(t, m.Layers, 1)

	l := m.Layers[0]
	require.Len(t, l.Chunks, 1)
	assert.Len(t, l.Chunks[0].Tiles, 4)

	assert.Equal(t, uint32(0), tilesetTileID(t, l.TileAt(-2, -2)))
	assert.True(t, l.TileAt(-1, -2).Nil, "gid 0 in XML chunk")
	assert.Equal(t, uint32(2), tilesetTileID(t, l.TileAt(-2, -1)))
	assert.Equal(t, uint32(3), tilesetTileID(t, l.TileAt(-1, -1)))
}

func TestInfiniteTileRegion(t *testing.T) {
	m := loadInfiniteFixture(t)
	floor := m.Layers[0]

	// Region straddling the row of tiles starting at (0,0).
	region := floor.TileRegion(0, 0, 4, 2)
	require.Len(t, region, 8)
	for i, want := range []uint32{0, 1, 2, 3, 4, 5, 6, 7} {
		assert.Equal(t, want, tilesetTileID(t, region[i]), "index %d", i)
	}

	// Empty chunk, partially covered region.
	region = floor.TileRegion(-17, -17, 3, 3)
	require.Len(t, region, 9)
	for i, tile := range region {
		assert.True(t, tile.IsNil(), "index %d", i)
	}

	// Region spanning the empty negative chunk and the data chunk.
	region = floor.TileRegion(-1, -1, 3, 3)
	require.Len(t, region, 9)
	assert.True(t, region[0].Nil)  // (-1,-1), in the all-empty chunk
	assert.True(t, region[1].Nil)  // (0,-1), no chunk
	assert.False(t, region[4].Nil) // (0,0), first gid of the data chunk
	assert.Equal(t, uint32(0), region[4].ID)
	assert.False(t, region[5].Nil) // (1,0)
	assert.Equal(t, uint32(1), region[5].ID)

	// Region over the gap between the two chunks of the base64 layer: all
	// cells are empty, even though a stored chunk is only a few tiles away.
	region = floor.TileRegion(7, -9, 3, 2)
	require.Len(t, region, 6)
	for i, tile := range region {
		assert.True(t, tile.IsNil(), "index %d", i)
	}
}

func TestInfiniteTileRegionAcrossChunks(t *testing.T) {
	m := loadInfiniteFixture(t)
	b64 := m.Layers[1]

	// Rows -8 and -7 of the (8,-8) 4x2 chunk, plus an empty row above it.
	region := b64.TileRegion(8, -9, 4, 3)
	require.Len(t, region, 12)
	for col := 0; col < 4; col++ {
		assert.True(t, region[col].Nil, "row -9 col %d should be empty", col)
	}
	assert.Equal(t, uint32(20), region[4].ID)
	assert.Equal(t, uint32(23), region[7].ID)
	for col := 0; col < 4; col++ {
		assert.True(t, region[8+col].Nil, "row -7 col %d should be empty", col)
	}

	// Invalid sizes return nil rather than allocating.
	assert.Nil(t, b64.TileRegion(0, 0, 0, 5))
	assert.Nil(t, b64.TileRegion(0, 0, 5, -1))
}

func TestInfiniteRegionMatchesTileAt(t *testing.T) {
	m := loadInfiniteFixture(t)
	for _, l := range m.Layers {
		const x0, y0, w, h = -20, -20, 45, 45
		region := l.TileRegion(x0, y0, w, h)
		require.Len(t, region, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				got := region[y*w+x]
				want := l.TileAt(x0+x, y0+y)
				require.Equal(t, want.IsNil(), got.IsNil(), "nil mismatch at (%d,%d)", x0+x, y0+y)
				if !want.IsNil() {
					assert.Equal(t, want.ID, got.ID, "id mismatch at (%d,%d)", x0+x, y0+y)
					assert.Equal(t, want.HorizontalFlip, got.HorizontalFlip)
					assert.Equal(t, want.VerticalFlip, got.VerticalFlip)
					assert.Equal(t, want.DiagonalFlip, got.DiagonalFlip)
					assert.Equal(t, want.RotatedHexagonal120, got.RotatedHexagonal120)
				}
			}
		}
	}
}

// TestFiniteMapLayersUnchanged pins the behaviour of the finite-map API that
// predates chunks: Tiles is still populated and TileAt mirrors it.
func TestFiniteMapLayersUnchanged(t *testing.T) {
	m, err := LoadFile(filepath.Join(GetAssetsDirectory(), "test2.tmx"))
	require.NoError(t, err)
	require.NotEmpty(t, m.Layers)

	for _, l := range m.Layers {
		assert.False(t, m.Infinite)
		assert.Nil(t, l.Chunks)
		assert.Len(t, l.Tiles, m.Width*m.Height)

		for i := 0; i < len(l.Tiles); i += 7 {
			x, y := i%m.Width, i/m.Width
			got := l.TileAt(x, y)
			want := l.Tiles[i]
			require.Equal(t, want.IsNil(), got.IsNil(), "(%d,%d)", x, y)
			if !want.IsNil() {
				assert.Equal(t, want.ID, got.ID)
			}
		}
	}

	// Out-of-range coordinates are empty rather than panicking.
	require.NotEmpty(t, m.Layers)
	l := m.Layers[0]
	assert.True(t, l.TileAt(-1, 0).Nil)
	assert.True(t, l.TileAt(0, -1).Nil)
	assert.True(t, l.TileAt(m.Width, 0).Nil)
	assert.True(t, l.TileAt(0, m.Height).Nil)
}

func TestFiniteMapTileRegion(t *testing.T) {
	m, err := LoadFile(filepath.Join(GetAssetsDirectory(), "test2.tmx"))
	require.NoError(t, err)
	l := m.Layers[0]

	region := l.TileRegion(0, 0, m.Width, m.Height)
	require.Len(t, region, len(l.Tiles))
	for i := range region {
		assert.Equal(t, l.Tiles[i].IsNil(), region[i].IsNil(), "index %d", i)
		if !l.Tiles[i].IsNil() {
			assert.Equal(t, l.Tiles[i].ID, region[i].ID)
		}
	}

	// The same region offset off the top-left corner: the overlap matches,
	// the rest is empty.
	region = l.TileRegion(-1, -1, 3, 3)
	require.Len(t, region, 9)
	assert.True(t, region[0].Nil)                          // (-1,-1)
	assert.True(t, region[1].Nil)                          // (0,-1)
	assert.Equal(t, l.Tiles[0].IsNil(), region[4].IsNil()) // (0,0) -> index 4
}

// TestChunkOverlapDetectionMatchesBruteForce cross-checks the sweep-line
// overlap check against a pairwise one on randomized chunk layouts, including
// negative coordinates, varied sizes, shared edges and exact duplicates.
func TestChunkOverlapDetectionMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261120))
	sizes := []int{1, 2, 8, 16, 32}

	brute := func(chunks []*Chunk) bool {
		for i := 0; i < len(chunks); i++ {
			for j := i + 1; j < len(chunks); j++ {
				a, b := chunks[i], chunks[j]
				if int64(a.X) < int64(b.X)+int64(b.Width) &&
					int64(b.X) < int64(a.X)+int64(a.Width) &&
					int64(a.Y) < int64(b.Y)+int64(b.Height) &&
					int64(b.Y) < int64(a.Y)+int64(a.Height) {
					return true
				}
			}
		}
		return false
	}

	for iter := 0; iter < 1000; iter++ {
		n := 1 + rng.Intn(25)
		chunks := make([]*Chunk, 0, n)
		seen := make(map[[4]int]bool, n)
		for len(chunks) < n {
			key := [4]int{
				rng.Intn(40) - 20,
				rng.Intn(40) - 20,
				sizes[rng.Intn(len(sizes))],
				sizes[rng.Intn(len(sizes))],
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			chunks = append(chunks, &Chunk{X: key[0], Y: key[1], Width: key[2], Height: key[3]})
		}

		sortChunks(chunks)
		a, b := findOverlap(chunks)
		if got, want := a != nil, brute(chunks); got != want {
			t.Fatalf("iteration %d: findOverlap found=%v (%v, %v), brute force=%v",
				iter, got, a, b, want)
		}
	}
}

func TestInfiniteErrors(t *testing.T) {
	const mapPrefix = `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="%s" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="%s">%s</data>
 </layer>
</map>`

	load := func(t *testing.T, infinite, encoding, data string) (*Map, error) {
		t.Helper()
		doc := fmt.Sprintf(mapPrefix, infinite, encoding, data)
		return LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	}

	t.Run("zero width chunk", func(t *testing.T) {
		_, err := load(t, "1", "csv", `<chunk x="0" y="0" width="0" height="2"></chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkZeroSize)
		assert.Contains(t, err.Error(), "layer")
	})

	t.Run("negative height chunk", func(t *testing.T) {
		_, err := load(t, "1", "csv", `<chunk x="0" y="0" width="2" height="-1"></chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkZeroSize)
	})

	t.Run("truncated csv", func(t *testing.T) {
		_, err := load(t, "1", "csv", `<chunk x="0" y="0" width="2" height="2">1,2,3</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDecodedTileCount)
		assert.Contains(t, err.Error(), "2x2")
	})

	t.Run("truncated base64", func(t *testing.T) {
		// One GID (4 bytes) for a 2x2 chunk needs 16 bytes.
		raw := base64.StdEncoding.EncodeToString([]byte{1, 0, 0, 0})
		_, err := load(t, "1", "base64", `<chunk x="0" y="0" width="2" height="2">`+raw+`</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDecodedTileCount)
	})

	t.Run("truncated XML", func(t *testing.T) {
		_, err := load(t, "1", "", `<chunk x="0" y="0" width="2" height="2"><tile gid="1"/><tile gid="0"/></chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDecodedTileCount)
	})

	t.Run("unknown compression", func(t *testing.T) {
		doc := `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="base64" compression="bzip2"><chunk x="0" y="0" width="1" height="1">AAAAAA==</chunk></data>
 </layer>
</map>`
		_, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnknownCompression)
	})

	t.Run("unknown encoding", func(t *testing.T) {
		// Unknown encodings are rejected on infinite maps even when no chunk
		// is present; finite maps keep their historical behaviour.
		_, err := load(t, "1", "hex", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnknownEncoding)
	})

	t.Run("overlapping chunks", func(t *testing.T) {
		_, err := load(t, "1", "csv", `
<chunk x="0" y="0" width="2" height="2">1,2,3,4</chunk>
<chunk x="1" y="1" width="2" height="2">5,6,7,8</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverlappingChunks)
		assert.Contains(t, err.Error(), "(0,0)")
		assert.Contains(t, err.Error(), "(1,1)")
	})

	t.Run("adjacent chunks are not overlapping", func(t *testing.T) {
		m, err := load(t, "1", "csv", `
<chunk x="0" y="0" width="2" height="2">1,2,3,4</chunk>
<chunk x="2" y="0" width="2" height="2">5,6,7,8</chunk>`)
		require.NoError(t, err)
		assert.Len(t, m.Layers[0].Chunks, 2)
	})

	t.Run("chunk data on finite map", func(t *testing.T) {
		_, err := load(t, "0", "csv", `<chunk x="0" y="0" width="2" height="2">1,2,3,4</chunk>`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not infinite")
	})

	t.Run("tile data outside a chunk", func(t *testing.T) {
		_, err := load(t, "1", "csv", `1,2,3,4`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkDataOutsideChunk)
	})

	t.Run("tile data alongside chunks", func(t *testing.T) {
		_, err := load(t, "1", "csv", `
1,2,3,4
<chunk x="0" y="0" width="2" height="2">1,2,3,4</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkDataOutsideChunk)
	})

	t.Run("iteration order independent overlap detection", func(t *testing.T) {
		// The overlapping chunk is written first in document order; validation
		// must not rely on the file's ordering.
		_, err := load(t, "1", "csv", `
<chunk x="4" y="4" width="4" height="4">`+strings.Repeat("1,", 15)+`1</chunk>
<chunk x="0" y="0" width="8" height="8">`+strings.Repeat("2,", 63)+`2</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrOverlappingChunks)
	})
}

// TestInfiniteChunkCoordinateLimits covers far-away chunks (allowed) and
// coordinates beyond the supported range (rejected).
func TestInfiniteChunkCoordinateLimits(t *testing.T) {
	load := func(t *testing.T, chunk string) (*Map, error) {
		t.Helper()
		doc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="csv">%s</data>
 </layer>
</map>`, chunk)
		return LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	}

	t.Run("far apart negative and positive chunks", func(t *testing.T) {
		m, err := load(t, `
<chunk x="-400000000" y="-400000000" width="1" height="1">1</chunk>
<chunk x="400000000" y="400000000" width="1" height="1">2</chunk>`)
		require.NoError(t, err)
		l := m.Layers[0]
		require.Len(t, l.Chunks, 2)
		assert.Equal(t, uint32(0), tilesetTileID(t, l.TileAt(-400000000, -400000000)))
		assert.Equal(t, uint32(1), tilesetTileID(t, l.TileAt(400000000, 400000000)))
		assert.True(t, l.TileAt(0, 0).Nil)
	})

	t.Run("coordinate beyond range", func(t *testing.T) {
		_, err := load(t, `<chunk x="600000000" y="0" width="1" height="1">1</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkOutOfBounds)
	})

	t.Run("positive edge in range", func(t *testing.T) {
		m, err := load(t, `<chunk x="536870911" y="0" width="1" height="1">1</chunk>`)
		require.NoError(t, err)
		assert.Equal(t, uint32(0), tilesetTileID(t, m.Layers[0].TileAt(536870911, 0)))
	})

	t.Run("chunk width overflowing int64 arithmetic", func(t *testing.T) {
		// x + width must not wrap around into the accepted range.
		_, err := load(t, `<chunk x="1" y="0" width="9223372036854775807" height="1">1</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkOutOfBounds)
	})

	t.Run("chunk crossing the range edge", func(t *testing.T) {
		_, err := load(t, `<chunk x="536870911" y="0" width="2" height="1">1,2</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChunkOutOfBounds)
	})
}

func TestInfiniteDuplicateChunksLastWins(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="csv">
   <chunk x="0" y="0" width="2" height="1">1,2</chunk>
   <chunk x="0" y="0" width="2" height="1">3,4</chunk>
  </data>
 </layer>
</map>`

	m, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	require.NoError(t, err)

	l := m.Layers[0]
	require.Len(t, l.Chunks, 1, "identical chunks collapse to one")
	assert.Equal(t, uint32(2), tilesetTileID(t, l.TileAt(0, 0)))
	assert.Equal(t, uint32(3), tilesetTileID(t, l.TileAt(1, 0)))
}

func TestInfiniteDecompressionLimit(t *testing.T) {
	old := MaxDecodedLayerDataSize
	MaxDecodedLayerDataSize = 64
	defer func() { MaxDecodedLayerDataSize = old }()

	// A gzip bomb: 16 KiB of zeros inside a 2x2 (16 byte) chunk declaration.
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := zw.Write(make([]byte, 16<<10))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	payload := base64.StdEncoding.EncodeToString(compressed.Bytes())

	load := func(t *testing.T, infinite, data string) error {
		t.Helper()
		doc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="%s" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="base64" compression="gzip">%s</data>
 </layer>
</map>`, infinite, data)
		_, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
		return err
	}

	t.Run("gzip bomb rejected", func(t *testing.T) {
		err := load(t, "1", `<chunk x="0" y="0" width="2" height="2">`+payload+`</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrLayerDataTooLarge)
	})

	t.Run("gzip bomb rejected for finite maps too", func(t *testing.T) {
		// A 0x0 finite map decodes the same payload through the finite path.
		err := load(t, "0", payload)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrLayerDataTooLarge)
	})

	t.Run("chunk cell count over limit rejected before decoding", func(t *testing.T) {
		// 17 cells need 68 bytes, over the 64-byte limit. The data is valid;
		// it must be rejected from the declared size alone.
		err := load(t, "1", `<chunk x="0" y="0" width="17" height="1">`+payload+`</chunk>`)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrLayerDataTooLarge)
	})
}

func TestInfiniteZlibCompression(t *testing.T) {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	gids := []uint32{41, 42, 43, 44}
	raw := make([]byte, 0, len(gids)*4)
	for _, gid := range gids {
		raw = append(raw, byte(gid), byte(gid>>8), byte(gid>>16), byte(gid>>24))
	}
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	payload := base64.StdEncoding.EncodeToString(compressed.Bytes())

	doc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="base64" compression="zlib"><chunk x="-1" y="-1" width="2" height="2">%s</chunk></data>
 </layer>
</map>`, payload)

	m, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	require.NoError(t, err)
	l := m.Layers[0]
	assert.Equal(t, uint32(40), tilesetTileID(t, l.TileAt(-1, -1)))
	assert.Equal(t, uint32(43), tilesetTileID(t, l.TileAt(0, 0)))
}

// TestInfiniteMissingTileset covers a chunk-only tile layer whose map
// declares no tileset at all: the error must say which layer, chunk and cell
// failed, and still wrap ErrInvalidTileGID.
func TestInfiniteMissingTileset(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <layer id="1" name="L" width="0" height="0">
  <data encoding="csv">
   <chunk x="-4" y="-4" width="2" height="2">0,0,0,1</chunk>
  </data>
 </layer>
</map>`

	_, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTileGID)

	msg := err.Error()
	assert.Contains(t, msg, `layer "L"`)
	assert.Contains(t, msg, "(-3,-3)", "global cell of the bad GID")
	assert.Contains(t, msg, "at (-4,-4) 2x2", "chunk origin and size")
}

// TestInfiniteMissingTilesetFile covers a chunk whose gid resolves to an
// external tileset file that cannot be opened; that error gains the same
// layer/chunk/cell context.
func TestInfiniteMissingTilesetFile(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="2" nextobjectid="1">
 <tileset firstgid="1" source="tilesets/does-not-exist.tsx"/>
 <layer id="1" name="L" width="0" height="0">
  <data encoding="csv">
   <chunk x="8" y="8" width="1" height="1">1</chunk>
  </data>
 </layer>
</map>`

	_, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `layer "L"`)
	assert.Contains(t, err.Error(), "cell (8,8)", "global cell")
	assert.Contains(t, err.Error(), "chunk 0 at (8,8) 1x1")
}

// TestInfiniteGroupLayer verifies chunked layers nested in group layers are
// decoded and queryable too.
func TestInfiniteGroupLayer(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" tiledversion="1.11.0" orientation="orthogonal" renderorder="right-down" width="0" height="0" tilewidth="16" tileheight="16" infinite="1" nextlayerid="3" nextobjectid="1">
 <tileset firstgid="1" name="ts" tilewidth="16" tileheight="16" tilecount="10" columns="10">
  <image source="unused.png" width="160" height="16"/>
 </tileset>
 <group id="1" name="G">
  <layer id="2" name="Inner" width="0" height="0">
   <data encoding="csv"><chunk x="-1" y="0" width="2" height="1">5,6</chunk></data>
  </layer>
 </group>
</map>`

	m, err := LoadReader(GetAssetsDirectory(), strings.NewReader(doc))
	require.NoError(t, err)
	require.Len(t, m.Groups, 1)
	require.Len(t, m.Groups[0].Layers, 1)

	l := m.Groups[0].Layers[0]
	require.Len(t, l.Chunks, 1)
	assert.Equal(t, uint32(4), tilesetTileID(t, l.TileAt(-1, 0)))
	assert.Equal(t, uint32(5), tilesetTileID(t, l.TileAt(0, 0)))
	assert.True(t, l.TileAt(1, 0).Nil)
}

// TestInfiniteFixtureDecodeErrors runs every malformed payload through the
// chunk decoder directly, which keeps the cases compact and independent of
// the XML layer.
func TestInfiniteChunkDecodeErrors(t *testing.T) {
	m := &Map{Tilesets: []*Tileset{{FirstGID: 1, SourceLoaded: true, TileCount: 10}}}

	t.Run("invalid csv value", func(t *testing.T) {
		c := &Chunk{X: 0, Y: 0, Width: 1, Height: 1, Encoding: "csv", rawData: []byte("99999999999")}
		err := c.decodeTiles(m, "L")
		require.Error(t, err)
	})

	t.Run("csv overflow", func(t *testing.T) {
		c := &Chunk{X: 0, Y: 0, Width: 1, Height: 1, Encoding: "csv", rawData: []byte("4294967296")}
		err := c.decodeTiles(m, "L")
		require.Error(t, err)
	})

	t.Run("gid without tileset", func(t *testing.T) {
		c := &Chunk{X: 0, Y: 0, Width: 1, Height: 1, Encoding: "csv", rawData: []byte("50")}
		err := c.decodeTiles(&Map{}, "L")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidTileGID))
		assert.Contains(t, err.Error(), "cell (0,0)")
	})
}
