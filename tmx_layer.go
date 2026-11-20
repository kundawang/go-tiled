/*
Copyright (c) 2017 Lauris Bukšis <lauris@nix.lv>

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
	"encoding/xml"
	"errors"
	"fmt"
	"image"
)

// NilLayerTile is reusable layer tile that is nil
var NilLayerTile = &LayerTile{Nil: true}

var (
	// ErrInvalidDecodedTileCount error is returned when layer data has invalid tile count
	ErrInvalidDecodedTileCount = errors.New("tiled: invalid decoded tile count")
	// ErrEmptyLayerData error is returned when layer contains no data
	ErrEmptyLayerData = errors.New("tiled: missing layer data")
	// ErrUnknownEncoding error is returned when kayer data has unknown encoding
	ErrUnknownEncoding = errors.New("tiled: unknown data encoding")
)

// LayerTile is a layer tile
type LayerTile struct {
	// Tile ID
	ID uint32
	// Tile tileset
	Tileset *Tileset
	// Horizontal tile image flip
	HorizontalFlip bool
	// Vertical tile image flip
	VerticalFlip bool
	// Diagonal tile image flip
	DiagonalFlip bool
	// RotatedHexagonal120 marks the tile as rotated 120 degrees clockwise.
	// Only meaningful on hexagonal (and staggered) maps, where it combines with
	// HorizontalFlip/VerticalFlip/DiagonalFlip to produce all 6 rotations of a
	// hex tile in 60-degree steps: none = 0, DiagonalFlip = 60, this = 120,
	// Horizontal+Vertical = 180, Horizontal+Vertical+DiagonalFlip = 240,
	// Horizontal+Vertical+this = 300. Always false for other orientations.
	RotatedHexagonal120 bool
	// Tile is nil
	Nil bool
}

// IsNil returs if tile is nil
func (t *LayerTile) IsNil() bool {
	return t.Nil
}

// Layer is a map layer
type Layer struct {
	_map *Map
	// Unique ID of the layer.
	// Each layer that added to a map gets a unique id. Even if a layer is deleted,
	// no layer ever gets the same ID. Can not be changed in Tiled. (since Tiled 1.2)
	ID uint32 `xml:"id,attr"`
	// The name of the layer.
	Name string `xml:"name,attr"`
	// The class of this layer (since 1.9, defaults to "").
	Class string `xml:"class,attr"`
	// The opacity of the layer as a value from 0 to 1. Defaults to 1.
	Opacity float32 `xml:"opacity,attr"`
	// Whether the layer is shown (1) or hidden (0). Defaults to 1.
	Visible bool `xml:"visible,attr"`
	// Rendering offset for this layer in pixels. Defaults to 0. (since 0.14)
	OffsetX float64 `xml:"offsetx,attr"`
	// Rendering offset for this layer in pixels. Defaults to 0. (since 0.14)
	OffsetY float64 `xml:"offsety,attr"`
	// The parallax x factor of the layer 0 - 1.0
	ParallaxX float32 `xml:"parallaxx,attr"`
	// The parallax y factor of the layer 0 - 1.0
	ParallaxY float32 `xml:"parallaxy,attr"`
	// The compositing mode to use for this layer. Valid values are normal,
	// multiply, multiply (legacy), screen, overlay, darken, lighten, color dodge,
	// color burn, hard light, soft light, difference, exclusion, hue, saturation,
	// color and luminosity. (since 1.12, defaults to normal)
	Mode string `xml:"mode,attr"`
	// Custom properties
	Properties Properties `xml:"properties>property"`
	// This is the attribute you'd like to use, not Data. Tile entry at (x,y) is obtained using l.DecodedTiles[y*map.Width+x].
	//
	// For infinite maps this is nil: their tile data lives in Chunks and must
	// be queried with TileAt or TileRegion.
	Tiles []*LayerTile
	// Chunks holds the decoded tile data of an infinite map's layer, sorted by
	// Y and then X. It is nil for finite maps, whose data is in Tiles.
	//
	// Chunk coordinates are global and may be negative. Chunks of one layer
	// must not partially overlap; exactly identical chunks are accepted and the
	// last one in document order is kept. Use TileAt or TileRegion for
	// point/region queries.
	Chunks []*Chunk
	// Data
	data *Data
	// Set when all entries of the layer are NilTile
	empty bool
	// Set when the layer's data was stored in chunks (infinite map).
	chunked bool
	// chunkMaxBottom[i] is the largest bottom edge (Y+Height) among
	// Chunks[0..i]; used to find the first candidate chunk for a query.
	chunkMaxBottom []int64
}

// IsEmpty checks if layer has tiles other than nil
func (l *Layer) IsEmpty() bool {
	return l.empty
}

func (l *Layer) decodeLayerXML() (gids []uint32, err error) {
	if len(l.data.DataTiles) != l._map.Width*l._map.Height {
		return []uint32{}, ErrInvalidDecodedTileCount
	}

	gids = make([]uint32, len(l.data.DataTiles))
	for i := 0; i < len(gids); i++ {
		gids[i] = l.data.DataTiles[i].GID
	}

	return gids, nil
}

func (l *Layer) decodeLayerCSV() ([]uint32, error) {
	gids, err := l.data.decodeCSV()
	if err != nil {
		return []uint32{}, err
	}

	if len(gids) != l._map.Width*l._map.Height {
		return []uint32{}, ErrInvalidDecodedTileCount
	}

	return gids, nil
}

func (l *Layer) decodeLayerBase64() ([]uint32, error) {
	dataBytes, err := l.data.decodeBase64(l._map.Width * l._map.Height * 4)
	if err != nil {
		return []uint32{}, err
	}

	if len(dataBytes) != l._map.Width*l._map.Height*4 {
		return []uint32{}, ErrInvalidDecodedTileCount
	}

	gids := make([]uint32, l._map.Width*l._map.Height)

	j := 0
	for y := 0; y < l._map.Height; y++ {
		for x := 0; x < l._map.Width; x++ {
			gid := uint32(dataBytes[j]) +
				uint32(dataBytes[j+1])<<8 +
				uint32(dataBytes[j+2])<<16 +
				uint32(dataBytes[j+3])<<24
			j += 4

			gids[y*l._map.Width+x] = gid
		}
	}

	return gids, nil
}

func (l *Layer) decodeTiles() error {
	var gids []uint32
	var err error
	switch l.data.Encoding {
	case "csv":
		if gids, err = l.decodeLayerCSV(); err != nil {
			return err
		}
	case "base64":
		if gids, err = l.decodeLayerBase64(); err != nil {
			return err
		}
	case "": // XML "encoding"
		if gids, err = l.decodeLayerXML(); err != nil {
			return err
		}
	default:
		return ErrUnknownEncoding
	}

	l.Tiles = make([]*LayerTile, len(gids))

	// Count non-empty tiles so the LayerTiles they need can be allocated
	// as a single backing slice instead of one heap allocation per tile.
	nonNil := 0
	for _, gid := range gids {
		if gid != 0 {
			nonNil++
		}
	}
	slab := make([]LayerTile, nonNil)

	si := 0
	for j, gid := range gids {
		if gid == 0 {
			l.Tiles[j] = NilLayerTile
			continue
		}

		t := &slab[si]
		si++
		if err = l._map.fillTileGID(gid, t); err != nil {
			return err
		}
		l.Tiles[j] = t
	}

	return nil
}

// DecodeLayer decodes layer data
func (l *Layer) DecodeLayer(m *Map) error {
	l._map = m
	if l.data == nil {
		return ErrEmptyLayerData
	}

	if m.Infinite {
		// Chunked data is never read through the fixed-width decoders above.
		if err := l.decodeChunks(); err != nil {
			return err
		}
	} else {
		if len(l.data.Chunks) != 0 {
			return fmt.Errorf("tiled: layer %q has <chunk> data but the map is not infinite", l.Name)
		}
		if err := l.decodeTiles(); err != nil {
			return err
		}
		l.empty = true
		for _, tile := range l.Tiles {
			if !tile.Nil {
				l.empty = false
				break
			}
		}
	}

	// Data is not needed anymore
	l.data = nil

	return nil
}

// UnmarshalXML decodes a single XML element beginning with the given start element.
func (l *Layer) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	item := aliasLayer{}
	item.SetDefaults()

	if err := d.DecodeElement(&item, &start); err != nil {
		return err
	}

	*l = (Layer)(item.internalLayer)
	l.data = item.Data

	return nil
}

// tileIndex returns the tile at row-major index i of Tiles, or NilLayerTile
// if i is out of range or the entry is nil.
func (l *Layer) tileIndex(i int) *LayerTile {
	if i < 0 || i >= len(l.Tiles) || l.Tiles[i] == nil {
		return NilLayerTile
	}
	return l.Tiles[i]
}

// GetTilePosition returns the x,y position of the tileID on the current layer.
//
// It is defined only for fixed-size layers: an infinite map has no row width
// (its layers store data in chunks), so (0, 0) is returned for those and for
// layers not yet decoded.
func (l *Layer) GetTilePosition(tileID int) (int, int) {
	if l._map == nil || l.chunked || l._map.Width <= 0 {
		return 0, 0
	}
	x := tileID % l._map.Width
	y := tileID / l._map.Width
	return int(l.OffsetX) + x*l._map.TileWidth, int(l.OffsetY) + y*l._map.TileHeight
}

// GetTileRect returns the rectangle that contains the Tile in the original Tileset.Image
func (t *LayerTile) GetTileRect() image.Rectangle {
	return t.Tileset.GetTileRect(t.ID)
}
