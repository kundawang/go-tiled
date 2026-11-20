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
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
)

var (
	// ErrUnknownCompression error is returned when file contains invalid compression method
	ErrUnknownCompression = errors.New("tiled: invalid compression method")
	// ErrLayerDataTooLarge is returned when decoded layer data (including the
	// output of decompression) would exceed MaxDecodedLayerDataSize.
	ErrLayerDataTooLarge = errors.New("tiled: decoded layer data exceeds maximum size")
)

// MaxDecodedLayerDataSize limits, in bytes, how much tile data a single layer
// or chunk may decode to. GIDs are 4 bytes each, so this also bounds the
// number of cells accepted in one chunk. It exists to keep a malformed or
// maliciously compressed map from exhausting memory; it is a variable so
// applications with unusually large maps can raise it.
//
// It is not safe to change this while maps are being loaded concurrently.
var MaxDecodedLayerDataSize = 64 << 20 // 64 MiB

// Data is raw data
type Data struct {
	// The encoding used to encode the tile layer data. When used, it can be "base64" and "csv" at the moment.
	Encoding string `xml:"encoding,attr"`
	// The compression used to compress the tile layer data. Tiled Qt supports "gzip" and "zlib".
	Compression string `xml:"compression,attr"`
	// Raw data. Only populated when Encoding is "csv" or "base64".
	RawData []byte `xml:",innerxml"`
	// Parsed tile elements. Only populated when Encoding is not set.
	DataTiles []DataTile `xml:"tile"`
	// Chunks holds the tile data of an infinite map's layer, in document
	// order. The chunk's Encoding and Compression are those of the enclosing
	// <data> element. For finite maps this is empty.
	Chunks []*Chunk `xml:"chunk"`
}

// DataTile defines the value of a single tile on a tile layer
type DataTile struct {
	// The global tile ID.
	GID uint32 `xml:"gid,attr"`
}

// UnmarshalXML implements a hand-rolled decode of the <data> element.
//
// A tile layer's data is either one large text blob (csv/base64) or,
// for the uncompressed per-tile XML encoding, thousands of <tile gid="N"/>
// child elements. Letting encoding/xml decode DataTiles via reflection
// (as driven by the struct tags above) means paying its per-element,
// per-attribute reflection cost for every tile; walking the token stream
// directly and parsing "gid" by hand avoids that and is significantly
// faster for large maps using this encoding. The struct tags are kept so
// Data/DataTile still unmarshal and marshal correctly (round-trip to XML)
// when used directly with encoding/xml outside of this package.
func (d *Data) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "encoding":
			d.Encoding = attr.Value
		case "compression":
			d.Compression = attr.Value
		}
	}

	if d.Encoding != "" {
		return d.decodeCharData(dec)
	}
	return d.decodeTileElements(dec)
}

// decodeCharData collects the element's text content into RawData or, when
// the element contains <chunk> children (infinite maps), into the chunks.
func (d *Data) decodeCharData(dec *xml.Decoder) error {
	var buf bytes.Buffer
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			buf.Write(t)
		case xml.StartElement:
			// The <data> element itself only holds chunks for infinite maps;
			// anything else inside it is ignored.
			if t.Name.Local != "chunk" {
				if err := dec.Skip(); err != nil {
					return err
				}
				continue
			}
			ch, err := d.decodeChunkElement(dec, t)
			if err != nil {
				return err
			}
			d.Chunks = append(d.Chunks, ch)
		case xml.EndElement:
			d.RawData = buf.Bytes()
			return nil
		}
	}
}

// decodeChunkElement reads a single <chunk> element. Encoding and compression
// are inherited from the enclosing <data> element. Text content is collected
// into the chunk's rawData; for the XML encoding, <tile> children are
// collected into its dataTiles.
func (d *Data) decodeChunkElement(dec *xml.Decoder, start xml.StartElement) (*Chunk, error) {
	ch := &Chunk{Encoding: d.Encoding, Compression: d.Compression}

	for _, attr := range start.Attr {
		var err error
		switch attr.Name.Local {
		case "x":
			err = parseIntAttr(attr.Value, &ch.X)
		case "y":
			err = parseIntAttr(attr.Value, &ch.Y)
		case "width":
			err = parseIntAttr(attr.Value, &ch.Width)
		case "height":
			err = parseIntAttr(attr.Value, &ch.Height)
		}
		if err != nil {
			return nil, fmt.Errorf("tiled: chunk at (%d,%d): invalid attribute %s: %w",
				ch.X, ch.Y, attr.Name.Local, err)
		}
	}

	var buf bytes.Buffer
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.CharData:
			buf.Write(t)
		case xml.StartElement:
			if t.Name.Local == "tile" {
				var dt DataTile
				if err := parseTileElement(dec, t, &dt); err != nil {
					return nil, err
				}
				ch.dataTiles = append(ch.dataTiles, dt)
				continue
			}
			// Chunks never nest, but skip unknown children defensively.
			if err := dec.Skip(); err != nil {
				return nil, err
			}
		case xml.EndElement:
			ch.rawData = buf.Bytes()
			return ch, nil
		}
	}
}

// parseIntAttr parses a decimal integer attribute.
func parseIntAttr(s string, dst *int) error {
	v, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// decodeTileElements parses the <tile gid="N"/> children directly,
// bypassing reflection-based attribute decoding. For infinite maps it also
// accepts <chunk> elements, whose tile children are kept per chunk.
func (d *Data) decodeTileElements(dec *xml.Decoder) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tile":
				var dt DataTile
				if err := parseTileElement(dec, t, &dt); err != nil {
					return err
				}
				d.DataTiles = append(d.DataTiles, dt)
			case "chunk":
				ch, err := d.decodeChunkElement(dec, t)
				if err != nil {
					return err
				}
				d.Chunks = append(d.Chunks, ch)
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			return nil
		}
	}
}

// parseTileElement reads the gid attribute of a <tile> element and consumes
// the element.
func parseTileElement(dec *xml.Decoder, start xml.StartElement, dt *DataTile) error {
	for _, attr := range start.Attr {
		if attr.Name.Local != "gid" {
			continue
		}
		v, err := strconv.ParseUint(attr.Value, 10, 32)
		if err != nil {
			return err
		}
		dt.GID = uint32(v)
	}
	return dec.Skip()
}

// decodeBase64 decodes (and, if applicable, decompresses) the raw data.
func (d *Data) decodeBase64(sizeHint int) (data []byte, err error) {
	return decodeBase64Data(d.RawData, d.Compression, sizeHint)
}

// decodeBase64Data decodes and, if compression is set, decompresses rawData.
// Decompression output is capped at MaxDecodedLayerDataSize bytes.
func decodeBase64Data(rawData []byte, compression string, sizeHint int) (data []byte, err error) {
	rawData = bytes.TrimSpace(rawData)
	r := bytes.NewReader(rawData)

	encr := base64.NewDecoder(base64.StdEncoding, r)

	var comr io.Reader
	switch compression {
	case "gzip":
		comr, err = gzip.NewReader(encr)
		if err != nil {
			return
		}
	case "zlib":
		comr, err = zlib.NewReader(encr)
		if err != nil {
			return
		}
	case "":
		comr = encr
	default:
		err = ErrUnknownCompression
		return
	}

	limit := MaxDecodedLayerDataSize
	if limit < 0 {
		limit = 0
	}
	// Never pre-size the buffer beyond the limit, even if the caller's hint
	// (derived from untrusted width/height attributes) is larger.
	capacity := sizeHint
	if capacity < 0 || capacity > limit {
		capacity = limit
	}

	// Read one byte past the limit so exceeding it can be distinguished from
	// legitimate data of exactly the maximum size.
	buf := bytes.NewBuffer(make([]byte, 0, capacity))
	n, err := buf.ReadFrom(io.LimitReader(comr, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if n > int64(limit) {
		return nil, ErrLayerDataTooLarge
	}
	return buf.Bytes(), nil
}

func (d *Data) decodeCSV() ([]uint32, error) {
	return decodeCSVData(d.RawData)
}

// decodeCSVData parses comma-separated GIDs from raw.
func decodeCSVData(raw []byte) ([]uint32, error) {
	// Estimate the tile count from the raw byte length
	gids := make([]uint32, 0, len(raw)/2+1)

	var cur uint64
	inNum := false
	for _, c := range raw {
		if c >= '0' && c <= '9' {
			cur = cur*10 + uint64(c-'0')
			if cur > math.MaxUint32 {
				return nil, strconv.ErrRange
			}
			inNum = true
			continue
		}
		if inNum {
			gids = append(gids, uint32(cur))
			cur = 0
			inNum = false
		}
	}
	if inNum {
		gids = append(gids, uint32(cur))
	}

	return gids, nil
}
