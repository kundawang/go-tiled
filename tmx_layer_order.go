/*
Copyright (c) 2024 Lauris Bukšis <lauris@nix.lv>

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

import "sort"

// LayerKind identifies the kind of a top-level layer referenced by an
// OrderedLayer.
type LayerKind uint8

const (
	// TileLayerKind is a regular tile layer (<layer>), stored in Map.Layers.
	TileLayerKind LayerKind = iota
	// ImageLayerKind is a top-level image layer (<imagelayer>), stored in Map.ImageLayers.
	ImageLayerKind
)

// OrderedLayer references a top-level tile or image layer. Index is the
// position of the layer in Map.Layers for TileLayerKind or in
// Map.ImageLayers for ImageLayerKind.
type OrderedLayer struct {
	// Kind selects which layer slice Index refers to.
	Kind LayerKind
	// Index is the index into Map.Layers or Map.ImageLayers.
	Index int
}

// OrderedLayers returns the map's top-level tile layers and image layers in
// the order in which they occur in the TMX document. This is the order Tiled
// composites the layers in, and is needed because go-tiled exposes tile and
// image layers through separate slices that lose their interleaving.
//
// Maps built programmatically rather than decoded from XML carry no document
// order. Such layers are merged by their unique layer ID; when IDs are equal
// or unset, tile layers are returned before image layers.
func (m *Map) OrderedLayers() []OrderedLayer {
	type layerRef struct {
		offset int64
		id     uint32
		kind   LayerKind
		index  int
	}

	refs := make([]layerRef, 0, len(m.Layers)+len(m.ImageLayers))
	allDecoded := len(m.Layers)+len(m.ImageLayers) > 0

	for i, layer := range m.Layers {
		if layer == nil {
			continue
		}
		refs = append(refs, layerRef{
			offset: layer.decodeOffset,
			id:     layer.ID,
			kind:   TileLayerKind,
			index:  i,
		})
		if layer.decodeOffset == 0 {
			allDecoded = false
		}
	}

	for i, layer := range m.ImageLayers {
		if layer == nil {
			continue
		}
		refs = append(refs, layerRef{
			offset: layer.decodeOffset,
			id:     layer.ID,
			kind:   ImageLayerKind,
			index:  i,
		})
		if layer.decodeOffset == 0 {
			allDecoded = false
		}
	}

	if allDecoded {
		// Decoder offsets are strictly monotonic in document order.
		sort.SliceStable(refs, func(i, j int) bool {
			return refs[i].offset < refs[j].offset
		})
	} else {
		// Deterministic fallback for in-memory maps: unique layer ID order,
		// then kind and slice index for ties.
		sort.SliceStable(refs, func(i, j int) bool {
			if refs[i].id != refs[j].id {
				return refs[i].id < refs[j].id
			}
			if refs[i].kind != refs[j].kind {
				return refs[i].kind < refs[j].kind
			}
			return refs[i].index < refs[j].index
		})
	}

	ordered := make([]OrderedLayer, len(refs))
	for i, ref := range refs {
		ordered[i] = OrderedLayer{Kind: ref.kind, Index: ref.index}
	}
	return ordered
}
