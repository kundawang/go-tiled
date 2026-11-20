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
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrChunkZeroSize is returned when a <chunk> element has a width or
	// height that is zero or negative.
	ErrChunkZeroSize = errors.New("tiled: chunk has non-positive size")
	// ErrChunkOutOfBounds is returned when a chunk's coordinates are outside
	// the range this package can index (see MaxChunkCoordinate).
	ErrChunkOutOfBounds = errors.New("tiled: chunk coordinates out of supported range")
	// ErrOverlappingChunks is returned when two chunks of the same layer
	// partially overlap. Exactly identical chunks are not an error; see
	// Layer.Chunks.
	ErrOverlappingChunks = errors.New("tiled: overlapping chunks")
	// ErrChunkDataOutsideChunk is returned when an infinite map's tile layer
	// contains tile data that is not inside a <chunk> element.
	ErrChunkDataOutsideChunk = errors.New("tiled: infinite layer has tile data outside a chunk")
)

// maxInt bounds this package's coordinate arithmetic.
const maxInt = int(^uint(0) >> 1)

// MaxChunkCoordinate bounds the chunk coordinates accepted by this package.
// A chunk must satisfy -MaxChunkCoordinate <= x, x+width <= MaxChunkCoordinate
// (and likewise for y and height). This keeps global cell coordinates well
// within the range of an int on all supported platforms and rejects typo'd or
// corrupt coordinate values early; it is not a limit on how many chunks a map
// may have or how far apart distinct chunks may be.
const MaxChunkCoordinate = 1 << 29

// Chunk is a rectangular block of tiles on an infinite map's layer.
//
// X and Y are global grid coordinates (they may be negative) and TileAt
// accepts global coordinates as well. Tiles is row-major with Width cells per
// row; empty cells are NilLayerTile.
type Chunk struct {
	// The x coordinate of the chunk in tiles.
	X int `xml:"x,attr"`
	// The y coordinate of the chunk in tiles.
	Y int `xml:"y,attr"`
	// The width of the chunk in tiles.
	Width int `xml:"width,attr"`
	// The height of the chunk in tiles.
	Height int `xml:"height,attr"`
	// The encoding inherited from the enclosing <data> element ("" for the
	// per-tile XML encoding).
	Encoding string `xml:"-"`
	// The compression inherited from the enclosing <data> element.
	Compression string `xml:"-"`
	// Decoded tiles, row-major, Width*Height entries.
	Tiles []*LayerTile `xml:"-"`

	// Raw tile data, only kept until the layer is decoded.
	rawData []byte
	// Parsed <tile> children, used for the XML encoding.
	dataTiles []DataTile
}

// Contains reports whether the global grid cell (x, y) lies inside the chunk.
func (c *Chunk) Contains(x, y int) bool {
	// Compare in int64 so a chunk constructed by hand with extreme values
	// cannot wrap its far edge around.
	return int64(x) >= int64(c.X) && int64(x) < int64(c.X)+int64(c.Width) &&
		int64(y) >= int64(c.Y) && int64(y) < int64(c.Y)+int64(c.Height)
}

// TileAt returns the tile at global grid coordinates (x, y). It returns
// NilLayerTile when the coordinates are outside the chunk or the cell is
// empty. Unlike Layer.TileAt it does not look at other chunks.
func (c *Chunk) TileAt(x, y int) *LayerTile {
	if !c.Contains(x, y) {
		return NilLayerTile
	}
	return c.tileIndex((y-c.Y)*c.Width + (x - c.X))
}

// IsEmpty reports whether the chunk contains only empty cells.
func (c *Chunk) IsEmpty() bool {
	for _, t := range c.Tiles {
		if t != nil && !t.Nil {
			return false
		}
	}
	return true
}

func (c *Chunk) tileIndex(i int) *LayerTile {
	if i < 0 || i >= len(c.Tiles) || c.Tiles[i] == nil {
		return NilLayerTile
	}
	return c.Tiles[i]
}

// cellError annotates a tile lookup failure with the layer and global cell it
// occurred at.
func cellError(layerName string, x, y int, gid uint32, err error) error {
	return fmt.Errorf("tiled: layer %q cell (%d,%d): gid %#x: %w", layerName, x, y, gid, err)
}

// hasTileData reports whether a <data> element carries tile data outside a
// chunk: csv/base64 text, or loose <tile> children.
func hasTileData(d *Data) bool {
	if len(d.DataTiles) != 0 {
		return true
	}
	for _, b := range d.RawData {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return true
		}
	}
	return false
}

// validate checks the chunk's geometry before any tile data is decoded.
func (c *Chunk) validate() error {
	if c.Width <= 0 || c.Height <= 0 {
		return fmt.Errorf("%w: %dx%d at (%d,%d)", ErrChunkZeroSize, c.Width, c.Height, c.X, c.Y)
	}

	const limit = int64(MaxChunkCoordinate)
	w, h := int64(c.Width), int64(c.Height)
	x, y := int64(c.X), int64(c.Y)

	// Bound the sizes before adding an origin to them: an attribute may hold
	// any int, so x+w could otherwise overflow int64.
	if w > 2*limit || h > 2*limit ||
		x < -limit || x+w > limit || y < -limit || y+h > limit {
		return fmt.Errorf("%w: %dx%d at (%d,%d) (limit %d)",
			ErrChunkOutOfBounds, c.Width, c.Height, c.X, c.Y, MaxChunkCoordinate)
	}

	// w and h are now at most 2*limit = 2^30, so this product fits in int64.
	cells := w * h
	if cells > int64(MaxDecodedLayerDataSize)/4 {
		return fmt.Errorf("%w: %dx%d at (%d,%d) has %d cells (limit %d)",
			ErrLayerDataTooLarge, c.Width, c.Height, c.X, c.Y, cells, MaxDecodedLayerDataSize/4)
	}

	return nil
}

// decodeGIDs decodes the chunk's global tile IDs, including flip flags, and
// verifies that the data holds exactly Width*Height values.
func (c *Chunk) decodeGIDs() ([]uint32, error) {
	cells := int64(c.Width) * int64(c.Height)
	expected := int(cells)

	var gids []uint32
	switch c.Encoding {
	case "csv":
		var err error
		gids, err = decodeCSVData(c.rawData)
		if err != nil {
			return nil, err
		}
	case "base64":
		if expected > MaxDecodedLayerDataSize/4 {
			return nil, fmt.Errorf("%w: base64 chunk has %d cells (limit %d)",
				ErrLayerDataTooLarge, expected, MaxDecodedLayerDataSize/4)
		}
		dataBytes, err := decodeBase64Data(c.rawData, c.Compression, expected*4)
		if err != nil {
			return nil, err
		}
		if len(dataBytes) != expected*4 {
			return nil, fmt.Errorf("%w: base64 chunk decoded to %d bytes, want %d",
				ErrInvalidDecodedTileCount, len(dataBytes), expected*4)
		}
		gids = make([]uint32, expected)
		for i := range gids {
			j := i * 4
			gids[i] = uint32(dataBytes[j]) |
				uint32(dataBytes[j+1])<<8 |
				uint32(dataBytes[j+2])<<16 |
				uint32(dataBytes[j+3])<<24
		}
	case "": // XML "encoding"
		if int64(len(c.dataTiles)) != cells {
			return nil, fmt.Errorf("%w: XML chunk has %d tiles, want %d",
				ErrInvalidDecodedTileCount, len(c.dataTiles), cells)
		}
		gids = make([]uint32, expected)
		for i := range c.dataTiles {
			gids[i] = c.dataTiles[i].GID
		}
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownEncoding, c.Encoding)
	}

	if int64(len(gids)) != cells {
		return nil, fmt.Errorf("%w: chunk has %d tiles, want %d",
			ErrInvalidDecodedTileCount, len(gids), cells)
	}

	return gids, nil
}

// decodeTiles resolves the chunk's GIDs against the map's tilesets.
func (c *Chunk) decodeTiles(m *Map, layerName string) error {
	gids, err := c.decodeGIDs()
	if err != nil {
		return err
	}

	c.Tiles = make([]*LayerTile, len(gids))

	// Allocate LayerTiles that are actually used from one slab, as Layer
	// decoding does.
	nonNil := 0
	for _, gid := range gids {
		if gid != 0 {
			nonNil++
		}
	}
	slab := make([]LayerTile, nonNil)

	si := 0
	for i, gid := range gids {
		if gid == 0 {
			c.Tiles[i] = NilLayerTile
			continue
		}

		t := &slab[si]
		si++
		if err := m.fillTileGID(gid, t); err != nil {
			return cellError(layerName, c.X+i%c.Width, c.Y+i/c.Width, gid, err)
		}
		c.Tiles[i] = t
	}

	// Raw data is no longer needed.
	c.rawData = nil
	c.dataTiles = nil

	return nil
}

// decodeChunks decodes an infinite map layer's chunked tile data.
//
// Chunks are validated and checked for overlaps before any tileset is
// loaded, so structural problems are reported even if the map also has
// missing tilesets. Exactly identical chunks are allowed: the last one in
// document order wins and only it is kept in Layer.Chunks. Partially
// overlapping chunks are rejected because which tile occupies the shared
// cells would otherwise be ambiguous.
func (l *Layer) decodeChunks() error {
	switch l.data.Encoding {
	case "", "csv", "base64":
	default:
		return fmt.Errorf("tiled: layer %q: %w: %q", l.Name, ErrUnknownEncoding, l.data.Encoding)
	}

	chunks := l.data.Chunks
	if len(chunks) == 0 {
		if hasTileData(l.data) {
			return fmt.Errorf("tiled: layer %q: %w", l.Name, ErrChunkDataOutsideChunk)
		}
		l.Chunks = nil
		l.chunked = true
		l.empty = true
		return nil
	}

	if hasTileData(l.data) {
		// Both chunks and whole-layer data: the latter has no position on an
		// infinite map, so refuse rather than silently ignore it.
		return fmt.Errorf("tiled: layer %q: %w", l.Name, ErrChunkDataOutsideChunk)
	}

	// Validate geometry and drop exact duplicates (last one wins).
	kept := make([]*Chunk, 0, len(chunks))
	byBounds := make(map[[4]int]int, len(chunks))
	for i, c := range chunks {
		if c == nil {
			continue
		}
		if err := c.validate(); err != nil {
			return fmt.Errorf("tiled: layer %q chunk %d at (%d,%d): %w",
				l.Name, i, c.X, c.Y, err)
		}

		key := [4]int{c.X, c.Y, c.Width, c.Height}
		if idx, ok := byBounds[key]; ok {
			kept[idx] = c
			continue
		}
		byBounds[key] = len(kept)
		kept = append(kept, c)
	}

	sortChunks(kept)

	if a, b := findOverlap(kept); a != nil {
		return fmt.Errorf(
			"tiled: layer %q: %w: chunk %dx%d at (%d,%d) overlaps chunk %dx%d at (%d,%d)",
			l.Name, ErrOverlappingChunks,
			a.Width, a.Height, a.X, a.Y, b.Width, b.Height, b.X, b.Y)
	}

	for i, c := range kept {
		if err := c.decodeTiles(l._map, l.Name); err != nil {
			return fmt.Errorf("tiled: layer %q chunk %d at (%d,%d) %dx%d: %w",
				l.Name, i, c.X, c.Y, c.Width, c.Height, err)
		}
	}

	l.Chunks = kept
	l.Tiles = nil
	l.chunked = true
	l.buildChunkIndex()
	l.empty = true
	for _, c := range kept {
		if !c.IsEmpty() {
			l.empty = false
			break
		}
	}

	return nil
}

// buildChunkIndex prepares the point-query index over l.Chunks, which must
// already be sorted by Y then X.
func (l *Layer) buildChunkIndex() {
	l.chunkMaxBottom = make([]int64, len(l.Chunks))
	var maxBottom int64 = -1 << 63
	for i, c := range l.Chunks {
		bottom := int64(c.Y) + int64(c.Height)
		if bottom > maxBottom {
			maxBottom = bottom
		}
		l.chunkMaxBottom[i] = maxBottom
	}
}

// chunkAt returns the chunk containing global cell (x, y), or nil. Chunks may
// not partially overlap, so at most one can match.
func (l *Layer) chunkAt(x, y int) *Chunk {
	// Find the first chunk whose running maximum bottom edge is below y.
	i := sort.Search(len(l.Chunks), func(i int) bool {
		return l.chunkMaxBottom[i] > int64(y)
	})
	for ; i < len(l.Chunks) && int64(l.Chunks[i].Y) <= int64(y); i++ {
		if l.Chunks[i].Contains(x, y) {
			return l.Chunks[i]
		}
	}
	return nil
}

// TileAt returns the tile at global grid coordinates (x, y), which may be
// negative. Cells without data and coordinates outside the map (for finite
// maps) return NilLayerTile.
//
// For finite maps the coordinates are looked up in Layer.Tiles using the map
// width, exactly as before; for infinite maps they are looked up in Layer.Chunks.
func (l *Layer) TileAt(x, y int) *LayerTile {
	if l.chunked {
		if c := l.chunkAt(x, y); c != nil {
			return c.TileAt(x, y)
		}
		return NilLayerTile
	}

	if l._map == nil || x < 0 || y < 0 || x >= l._map.Width || y >= l._map.Height {
		return NilLayerTile
	}
	return l.tileIndex(y*l._map.Width + x)
}

// TileRegion returns the tiles covering the global grid cells from (x, y) up
// to but not including (x+width, y+height), in row-major order (width entries
// per row). Coordinates may be negative. Cells without data are NilLayerTile.
//
// It returns nil if width or height is not positive, if the resulting slice
// would overflow an int, or if it would exceed MaxDecodedLayerDataSize/4
// cells (the same per-layer size limit used when loading).
func (l *Layer) TileRegion(x, y, width, height int) []*LayerTile {
	if width <= 0 || height <= 0 {
		return nil
	}
	cells := int64(width) * int64(height)
	if cells > int64(MaxDecodedLayerDataSize)/4 {
		return nil
	}
	// The multiplication above is safe for any int inputs, but the loops below
	// compute x+col / y+row; reject regions whose far edge isn't representable
	// as an int rather than letting them wrap. width and height are bounded by
	// the cell limit, so the subtractions cannot overflow.
	if x > maxInt-width || y > maxInt-height {
		return nil
	}

	tiles := make([]*LayerTile, int(cells))
	for i := range tiles {
		tiles[i] = NilLayerTile
	}

	if !l.chunked {
		if l._map == nil {
			return tiles
		}
		w, h := l._map.Width, l._map.Height
		for row := 0; row < height; row++ {
			gy := y + row
			if gy < 0 || gy >= h {
				continue
			}
			for col := 0; col < width; col++ {
				gx := x + col
				if gx < 0 || gx >= w {
					continue
				}
				tiles[row*width+col] = l.tileIndex(gy*w + gx)
			}
		}
		return tiles
	}

	// Fill from the chunks intersecting the region instead of doing a lookup
	// per cell, so sparsely chunked maps don't pay for empty chunks.
	right := int64(x) + int64(width)
	bottom := int64(y) + int64(height)
	i := sort.Search(len(l.Chunks), func(i int) bool {
		return l.chunkMaxBottom[i] > int64(y)
	})
	for ; i < len(l.Chunks) && int64(l.Chunks[i].Y) < bottom; i++ {
		c := l.Chunks[i]
		cRight := int64(c.X) + int64(c.Width)
		if cRight <= int64(x) || int64(c.X) >= right {
			continue
		}
		x0, x1 := max(x, c.X), min(x+width, c.X+c.Width)
		y0, y1 := max(y, c.Y), min(y+height, c.Y+c.Height)
		for gy := y0; gy < y1; gy++ {
			for gx := x0; gx < x1; gx++ {
				tiles[(gy-y)*width+(gx-x)] = c.TileAt(gx, gy)
			}
		}
	}

	return tiles
}
