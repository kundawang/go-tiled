/*
Copyright (c) 2021 Lauris Bukšis <lauris@nix.lv>

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
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	// ErrTemplateCycle is returned when template files reference each other
	// in a circular way, directly or through a chain of nested templates.
	ErrTemplateCycle = errors.New("tiled: circular template reference")
	// ErrTemplateNotLoaded is returned when computing the effective view of
	// an object whose template was referenced but never loaded.
	ErrTemplateNotLoaded = errors.New("tiled: object template not loaded")
	// ErrPropertyTypeConflict is returned when an instance property overrides
	// a template property of the same name with an incompatible type.
	ErrPropertyTypeConflict = errors.New("tiled: template property type conflict")
	// ErrInvalidProperty is returned when a property has an empty name, an
	// unknown type or a value that does not match its declared type.
	ErrInvalidProperty = errors.New("tiled: invalid property")
)

// Names of the object fields that can be queried with EffectiveObject.Explicit.
const (
	ObjectFieldName       = "name"
	ObjectFieldClass      = "class"
	ObjectFieldX          = "x"
	ObjectFieldY          = "y"
	ObjectFieldWidth      = "width"
	ObjectFieldHeight     = "height"
	ObjectFieldRotation   = "rotation"
	ObjectFieldGID        = "gid"
	ObjectFieldVisible    = "visible"
	ObjectFieldProperties = "properties"
)

// EffectiveObject is the effective view of an Object: the result of merging
// the object instance with the template chain it references (if any).
//
// Fields explicitly set on the instance take precedence over the template;
// fields the instance omitted are inherited from the template chain. Because
// presence is tracked per XML attribute, an explicit zero value on the
// instance (x="0", name="", visible="0") wins over any template value, while
// an omitted attribute never masks the template.
//
// The original Object and Template remain untouched and inspectable through
// the Object and Template fields.
type EffectiveObject struct {
	// Object is the instance object as loaded from the map.
	Object *Object
	// Template is the root template the object references, or nil when the
	// object has no template.
	Template *Template

	// ID is the unique ID of the instance object (never inherited).
	ID uint32
	// Name is the effective name of the object.
	Name string
	// Class is the effective class of the object. The deprecated pre-1.9
	// "type" attribute is treated as the same logical field.
	Class string
	// Effective geometry fields.
	X        float64
	Y        float64
	Width    float64
	Height   float64
	Rotation float64
	// GID is the effective reference to a tile (0 when none).
	GID uint32
	// Visible reports whether the object is effectively shown.
	Visible bool
	// Properties are the merged custom properties: instance properties
	// override template properties with the same name, all others are
	// inherited.
	Properties Properties
	// Effective shape members. Instance shapes win as a whole; when the
	// instance defines none, the template shapes are inherited.
	Ellipses  []*Ellipse
	Polygons  []*Polygon
	PolyLines []*PolyLine
	Text      *Text

	// explicit records which fields were explicitly set on the instance
	// object itself (as opposed to inherited or defaulted).
	explicit map[string]bool
}

// Explicit reports whether the given field (one of the ObjectField*
// constants) was explicitly set on the instance object itself, rather than
// inherited from a template or left at its default. This is what allows
// distinguishing an omitted attribute from an explicit zero, empty string or
// false value.
func (e *EffectiveObject) Explicit(field string) bool {
	return e.explicit[field]
}

// Effective returns the effective view of the object, merging it with the
// template chain it references. Objects without a template simply expose
// their own values. The raw Object and Template are not modified.
func (o *Object) Effective() (*EffectiveObject, error) {
	return o.effective(make(map[*Object]bool))
}

func (o *Object) effective(seen map[*Object]bool) (*EffectiveObject, error) {
	if seen[o] {
		return nil, fmt.Errorf("%w: object id %d", ErrTemplateCycle, o.ID)
	}
	seen[o] = true
	defer delete(seen, o)

	if o.Template == nil && len(o.TemplateSource) > 0 {
		return nil, fmt.Errorf("%w: object id %d references %q", ErrTemplateNotLoaded, o.ID, o.TemplateSource)
	}

	var base *EffectiveObject
	if o.Template != nil && o.Template.Object != nil {
		var err error
		if base, err = o.Template.Object.effective(seen); err != nil {
			return nil, err
		}
	}

	e := &EffectiveObject{
		Object:   o,
		Template: o.Template,
		ID:       o.ID,
		explicit: make(map[string]bool),
	}

	var baseProps Properties
	if base != nil {
		e.Name = base.Name
		e.Class = base.Class
		e.X, e.Y = base.X, base.Y
		e.Width, e.Height = base.Width, base.Height
		e.Rotation = base.Rotation
		e.GID = base.GID
		e.Visible = base.Visible
		e.Ellipses = base.Ellipses
		e.Polygons = base.Polygons
		e.PolyLines = base.PolyLines
		e.Text = base.Text
		baseProps = base.Properties
	} else {
		// No template below: start from the object's own (defaulted) values.
		e.Name = o.Name
		if class, ok := objectClass(o); ok {
			e.Class = class
		}
		e.X, e.Y = o.X, o.Y
		e.Width, e.Height = o.Width, o.Height
		e.Rotation = o.Rotation
		e.GID = o.GID
		e.Visible = o.Visible
	}

	if base != nil {
		// Overlay fields explicitly set on this object.
		if o.isSet(ObjectFieldName) {
			e.Name = o.Name
			e.explicit[ObjectFieldName] = true
		}
		if class, ok := objectClass(o); ok {
			e.Class = class
			e.explicit[ObjectFieldClass] = true
		}
		if o.isSet(ObjectFieldX) {
			e.X = o.X
			e.explicit[ObjectFieldX] = true
		}
		if o.isSet(ObjectFieldY) {
			e.Y = o.Y
			e.explicit[ObjectFieldY] = true
		}
		if o.isSet(ObjectFieldWidth) {
			e.Width = o.Width
			e.explicit[ObjectFieldWidth] = true
		}
		if o.isSet(ObjectFieldHeight) {
			e.Height = o.Height
			e.explicit[ObjectFieldHeight] = true
		}
		if o.isSet(ObjectFieldRotation) {
			e.Rotation = o.Rotation
			e.explicit[ObjectFieldRotation] = true
		}
		if o.isSet(ObjectFieldGID) {
			e.GID = o.GID
			e.explicit[ObjectFieldGID] = true
		}
		if o.isSet(ObjectFieldVisible) {
			e.Visible = o.Visible
			e.explicit[ObjectFieldVisible] = true
		}
	} else {
		for _, field := range []string{
			ObjectFieldName, ObjectFieldClass, ObjectFieldX, ObjectFieldY,
			ObjectFieldWidth, ObjectFieldHeight, ObjectFieldRotation,
			ObjectFieldGID, ObjectFieldVisible,
		} {
			if o.isSet(field) {
				e.explicit[field] = true
			}
		}
	}

	if len(o.Ellipses) > 0 {
		e.Ellipses = o.Ellipses
	}
	if len(o.Polygons) > 0 {
		e.Polygons = o.Polygons
	}
	if len(o.PolyLines) > 0 {
		e.PolyLines = o.PolyLines
	}
	if o.Text != nil {
		e.Text = o.Text
	}

	props, err := mergeProperties(baseProps, o.Properties, o)
	if err != nil {
		return nil, err
	}
	e.Properties = props
	if len(o.Properties) > 0 {
		e.explicit[ObjectFieldProperties] = true
	}

	return e, nil
}

// EffectiveObjects returns the effective views of all objects in the group.
func (g *ObjectGroup) EffectiveObjects() ([]*EffectiveObject, error) {
	effective := make([]*EffectiveObject, 0, len(g.Objects))
	for _, o := range g.Objects {
		e, err := o.Effective()
		if err != nil {
			return nil, err
		}
		effective = append(effective, e)
	}
	return effective, nil
}

// objectClass returns the class of a single object, treating the deprecated
// pre-1.9 "type" attribute as the same logical field. The second return
// value reports whether either attribute was explicitly set.
func objectClass(o *Object) (string, bool) {
	if o.isSet(ObjectFieldClass) {
		return o.Class, true
	}
	if o.isSet("type") {
		return o.Type, true
	}
	return "", false
}

// mergeProperties merges instance properties over template (or already
// merged) base properties. An instance property replaces a base property
// with the same name; all other base properties are inherited.
func mergeProperties(base, override Properties, o *Object) (Properties, error) {
	merged := make(Properties, 0, len(base)+len(override))
	merged = append(merged, base...)

	for _, p := range override {
		if err := validateProperty(p); err != nil {
			return nil, fmt.Errorf("tiled: object id %d: %w", o.ID, err)
		}
		idx := -1
		for i, bp := range merged {
			if bp.Name == p.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			merged = append(merged, p)
			continue
		}
		if normalizePropertyType(merged[idx].Type) != normalizePropertyType(p.Type) {
			return nil, fmt.Errorf("%w: property %q on object id %d: template type %q, instance type %q",
				ErrPropertyTypeConflict, p.Name, o.ID, merged[idx].Type, p.Type)
		}
		merged[idx] = p
	}

	for _, p := range merged {
		if err := validateProperty(p); err != nil {
			return nil, fmt.Errorf("tiled: object id %d: %w", o.ID, err)
		}
	}

	return merged, nil
}

// normalizePropertyType maps equivalent property types to a canonical name
// for comparison purposes.
func normalizePropertyType(t string) string {
	switch t {
	case "":
		return "string"
	case "boolean":
		return "bool"
	default:
		return t
	}
}

// validateProperty checks that a property has a name, a known type and a
// value matching its declared type.
func validateProperty(p *Property) error {
	if p.Name == "" {
		return fmt.Errorf("%w: property with empty name", ErrInvalidProperty)
	}
	switch p.Type {
	case "", "string", "file", "object", "class":
		// Any value is valid.
	case "int":
		if _, err := strconv.ParseInt(p.Value, 10, 64); err != nil {
			return fmt.Errorf("%w: property %q: invalid int value %q", ErrInvalidProperty, p.Name, p.Value)
		}
	case "float":
		if _, err := strconv.ParseFloat(p.Value, 64); err != nil {
			return fmt.Errorf("%w: property %q: invalid float value %q", ErrInvalidProperty, p.Name, p.Value)
		}
	case "bool", "boolean":
		switch p.Value {
		case "true", "false", "0", "1":
		default:
			return fmt.Errorf("%w: property %q: invalid bool value %q", ErrInvalidProperty, p.Name, p.Value)
		}
	case "color":
		s := strings.TrimPrefix(p.Value, "#")
		if len(s) != 6 && len(s) != 8 {
			return fmt.Errorf("%w: property %q: invalid color value %q", ErrInvalidProperty, p.Name, p.Value)
		}
		if _, err := hex.DecodeString(s); err != nil {
			return fmt.Errorf("%w: property %q: invalid color value %q", ErrInvalidProperty, p.Name, p.Value)
		}
	default:
		return fmt.Errorf("%w: property %q: unknown type %q", ErrInvalidProperty, p.Name, p.Type)
	}
	return nil
}
