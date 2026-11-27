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

package render

import (
	"image"
	"image/color"
	"image/draw"
	"reflect"

	"github.com/lafriks/go-tiled"
)

// RenderImageLayer renders the single top-level image layer identified by
// id (its index into Map.ImageLayers). Unlike RenderVisibleLayers, hidden
// layers are rendered as well, matching RenderLayer and RenderObjectGroup.
//
// For flexibility the argument may be either the layer index (int) or the
// *tiled.ImageLayer itself. The layer offset, opacity and the
// RepeatX/RepeatY tiling settings are applied. Any error while opening or
// decoding the image is returned.
func (r *Renderer) RenderImageLayer(idOrLayer any) error {
	switch v := idOrLayer.(type) {
	case nil:
		return ErrOutOfBounds
	case *tiled.ImageLayer:
		if v == nil {
			return ErrOutOfBounds
		}
		return r._renderImageLayer(v)
	case tiled.ImageLayer:
		return r._renderImageLayer(&v)
	case int:
		return r.renderImageLayerIndex(v)
	}

	// Accept any other built-in or named integer kind (e.g. uint32 layer IDs
	// or a custom index type).
	rv := reflect.ValueOf(idOrLayer)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return r.renderImageLayerIndex(int(rv.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return r.renderImageLayerIndex(int(rv.Uint()))
	default:
		return ErrOutOfBounds
	}
}

func (r *Renderer) renderImageLayerIndex(id int) error {
	if id < 0 || id >= len(r.m.ImageLayers) {
		return ErrOutOfBounds
	}
	return r._renderImageLayer(r.m.ImageLayers[id])
}

// RenderVisibleImageLayers renders all visible top-level image layers in
// their slice order.
func (r *Renderer) RenderVisibleImageLayers() error {
	for _, layer := range r.m.ImageLayers {
		if !layer.Visible {
			continue
		}
		if err := r._renderImageLayer(layer); err != nil {
			return err
		}
	}
	return nil
}

// RenderImageLayers renders all top-level image layers in their slice order,
// including hidden ones.
func (r *Renderer) RenderImageLayers() error {
	for _, layer := range r.m.ImageLayers {
		if err := r._renderImageLayer(layer); err != nil {
			return err
		}
	}
	return nil
}

// renderImageLayer is an alias of _renderImageLayer for callers that prefer
// the non-underscore naming.
func (r *Renderer) renderImageLayer(layer *tiled.ImageLayer) error {
	return r._renderImageLayer(layer)
}

func (r *Renderer) _renderImageLayer(layer *tiled.ImageLayer) error {
	if layer == nil || layer.Image == nil {
		// Nothing determinate to draw without an image reference.
		return nil
	}

	img, err := r.getImageLayerImage(layer)
	if err != nil {
		return err
	}
	if img == nil {
		return nil
	}

	r.drawImageLayer(layer, img)
	return nil
}

func (r *Renderer) getImageLayerImage(layer *tiled.ImageLayer) (image.Image, error) {
	if layer.Image == nil || layer.Image.Source == "" {
		// Embedded image data is not currently decoded here; an image layer
		// without an external source has nothing renderable.
		return nil, nil
	}

	source := r.m.GetFileFullPath(layer.Image.Source)
	f, err := r.open(source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}

	if layer.Image.Trans != nil {
		img = r.applyImageLayerTransparency(img, layer.Image.Trans)
	}

	return img, nil
}

// drawImageLayer composites img according to the layer offset, opacity and
// independent X/Y repetition settings. All drawing is clipped to the render
// result bounds by the image/draw package.
func (r *Renderer) drawImageLayer(layer *tiled.ImageLayer, img image.Image) {
	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()
	if imgW <= 0 || imgH <= 0 {
		return
	}

	// OffsetX/OffsetY are the modern TMX attributes; X/Y are their deprecated
	// predecessors and never meaningfully combined with the modern ones.
	originX := layer.OffsetX + layer.X
	originY := layer.OffsetY + layer.Y

	canvas := r.Result.Bounds()

	xOrigins := r.imageLayerTileOrigins(originX, imgW, canvas.Min.X, canvas.Max.X, layer.RepeatX)
	yOrigins := r.imageLayerTileOrigins(originY, imgH, canvas.Min.Y, canvas.Max.Y, layer.RepeatY)

	var mask image.Image
	opacity := layer.EffectiveOpacity()
	opaque := opacity >= 1
	if !opaque {
		mask = image.NewUniform(color.Alpha{uint8(opacity * 255)})
	}

	for _, y := range yOrigins {
		for _, x := range xOrigins {
			dest := image.Rect(x, y, x+imgW, y+imgH)
			if opaque {
				draw.Draw(r.Result, dest, img, bounds.Min, draw.Over)
				continue
			}
			draw.DrawMask(r.Result, dest, img, bounds.Min, mask, mask.Bounds().Min, draw.Over)
		}
	}
}

// imageLayerTileOrigins returns the coordinates at which to place copies of
// an axis-sized tile of length size along one axis. When repeat is false only
// the layer origin is returned. When repeat is true it returns the origins of
// every copy intersecting the [min,max) canvas interval, including copies on
// the negative side of the origin so the interval is fully covered for any
// offset (positive or negative).
func (r *Renderer) imageLayerTileOrigins(origin, size, min, max int, repeat bool) []int {
	if !repeat || size <= 0 {
		return []int{origin}
	}

	// First copy whose end is past min: origin+k*size <= min < origin+(k+1)*size.
	delta := min - origin
	k := delta / size
	if delta < 0 && delta%size != 0 {
		k-- // floor division for a negative numerator
	}

	origins := make([]int, 0, (max-min)/size+2)
	for pos := origin + k*size; pos < max; k++ {
		origins = append(origins, pos)
		pos = origin + (k+1)*size
	}
	return origins
}

// applyImageLayerTransparency returns an NRGBA copy of img in which pixels
// matching the TMX "trans" color are fully transparent.
func (r *Renderer) applyImageLayerTransparency(img image.Image, trans *tiled.HexColor) image.Image {
	if img == nil || trans == nil {
		return img
	}

	tr, tg, tb, _ := trans.RGBA()
	keyR := uint8(tr >> 8)
	keyG := uint8(tg >> 8)
	keyB := uint8(tb >> 8)

	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)
	draw.Draw(out, bounds, img, bounds.Min, draw.Src)

	for i := 0; i < len(out.Pix); i += 4 {
		if out.Pix[i+0] == keyR && out.Pix[i+1] == keyG && out.Pix[i+2] == keyB {
			out.Pix[i+3] = 0
		}
	}
	return out
}
