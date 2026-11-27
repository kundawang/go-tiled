/*
   Copyright 2021 Google LLC.
   SPDX-License-Identifier: Apache-2.0
*/

package tiled_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lafriks/go-tiled"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveObjectTemplateMerge(t *testing.T) {
	m, err := tiled.LoadFile("assets/test_template_map.tmx")
	require.NoError(t, err)
	require.NotEmpty(t, m.ObjectGroups)

	objects := m.ObjectGroups[0].Objects
	require.Len(t, objects, 5)

	// Object 1: instance only overrides health=20 and x=0; everything else
	// must be inherited from the template.
	obj := objects[0]
	assert.Equal(t, "", obj.Name, "raw instance name must stay empty")
	require.NotNil(t, obj.Template)
	require.NotNil(t, obj.Template.Object)
	assert.Equal(t, "Goblin", obj.Template.Object.Name, "raw template object must stay inspectable")

	eff, err := obj.Effective()
	require.NoError(t, err)

	assert.Equal(t, "Goblin", eff.Name)
	assert.False(t, eff.Explicit(tiled.ObjectFieldName), "name is inherited, not explicit")
	assert.Equal(t, "enemy", eff.Class)
	assert.Equal(t, float64(0), eff.X, "explicit x=0 on the instance must win over template x=5")
	assert.True(t, eff.Explicit(tiled.ObjectFieldX), "x=0 must be reported as explicitly set")
	assert.Equal(t, float64(32), eff.Y)
	assert.True(t, eff.Explicit(tiled.ObjectFieldY))
	assert.Equal(t, float64(16), eff.Width)
	assert.False(t, eff.Explicit(tiled.ObjectFieldWidth))
	assert.Equal(t, float64(16), eff.Height)
	assert.Equal(t, float64(15), eff.Rotation, "rotation is inherited from the template")
	assert.True(t, eff.Visible)

	assert.Equal(t, 20, eff.Properties.GetInt("health"), "instance property overrides template")
	assert.Equal(t, 2, eff.Properties.GetInt("speed"), "template property is inherited")
	assert.Len(t, eff.Properties, 2)
	assert.Len(t, obj.Properties, 1, "raw instance properties are untouched")
	assert.Len(t, obj.Template.Object.Properties, 2, "raw template properties are untouched")

	// Object 2: no template at all — effective view is the object itself.
	plain, err := objects[1].Effective()
	require.NoError(t, err)
	assert.Nil(t, plain.Template)
	assert.Equal(t, "Plain", plain.Name)
	assert.Equal(t, float64(4), plain.X)
	assert.Equal(t, float64(8), plain.Width)
	assert.True(t, plain.Explicit(tiled.ObjectFieldName))

	// Object 3: explicit visible="0" (false) and width override.
	hidden, err := objects[2].Effective()
	require.NoError(t, err)
	assert.False(t, hidden.Visible, "explicit visible=0 must not be confused with an omitted attribute")
	assert.True(t, hidden.Explicit(tiled.ObjectFieldVisible))
	assert.Equal(t, float64(24), hidden.Width)
	assert.True(t, hidden.Explicit(tiled.ObjectFieldWidth))
	assert.Equal(t, "Goblin", hidden.Name)

	// Object 4: explicit empty name must override the template name.
	unnamed, err := objects[3].Effective()
	require.NoError(t, err)
	assert.Equal(t, "", unnamed.Name)
	assert.True(t, unnamed.Explicit(tiled.ObjectFieldName), "explicit empty name must be reported as explicit")

	// Object 5: nested template chain (grunt -> enemy).
	grunt, err := objects[4].Effective()
	require.NoError(t, err)
	assert.Equal(t, "Grunt", grunt.Name, "name from the nearest template wins")
	assert.Equal(t, "enemy", grunt.Class, "class inherited through the chain")
	assert.Equal(t, 10, grunt.Properties.GetInt("health"), "health inherited from the base template")
	assert.Equal(t, 3, grunt.Properties.GetInt("speed"), "speed overridden by the intermediate template")
	assert.Equal(t, float64(40), grunt.X)

	// Object 6: templated object nested inside a group layer.
	require.Len(t, m.Groups, 1)
	require.Len(t, m.Groups[0].ObjectGroups, 1)
	grouped := m.Groups[0].ObjectGroups[0].Objects[0]
	assert.True(t, grouped.TemplateLoaded, "templates of objects inside groups must be loaded")
	groupEff, err := grouped.Effective()
	require.NoError(t, err)
	assert.Equal(t, "Goblin", groupEff.Name)
	assert.Equal(t, 10, groupEff.Properties.GetInt("health"))
}

func TestEffectiveObjectGroupEffectiveObjects(t *testing.T) {
	m, err := tiled.LoadFile("assets/test_template_map.tmx")
	require.NoError(t, err)

	effective, err := m.ObjectGroups[0].EffectiveObjects()
	require.NoError(t, err)
	require.Len(t, effective, 5)
	assert.Equal(t, "Goblin", effective[0].Name)
	assert.Equal(t, "Plain", effective[1].Name)
}

func TestEffectiveObjectTemplateCycle(t *testing.T) {
	_, err := tiled.LoadFile("assets/test_template_cycle.tmx")
	require.Error(t, err)
	assert.True(t, errors.Is(err, tiled.ErrTemplateCycle), "expected ErrTemplateCycle, got: %v", err)
	assert.Contains(t, err.Error(), "test_template_cycle")
}

func TestEffectiveObjectTemplateMissing(t *testing.T) {
	_, err := tiled.LoadFile("assets/test_template_missing.tmx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does_not_exist.tx")
	assert.Contains(t, err.Error(), "object id 1")
}

func TestEffectiveObjectPropertyTypeConflict(t *testing.T) {
	const mapXML = `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" orientation="orthogonal" renderorder="right-down" width="10" height="10" tilewidth="32" tileheight="32" infinite="0" nextlayerid="2" nextobjectid="2">
 <objectgroup id="1" name="Objects">
  <object id="1" template="test_template_enemy.tx" x="0" y="0">
   <properties>
    <property name="health" type="string" value="twenty"/>
   </properties>
  </object>
 </objectgroup>
</map>`

	m, err := tiled.LoadReader("assets", strings.NewReader(mapXML))
	require.NoError(t, err)

	_, err = m.ObjectGroups[0].Objects[0].Effective()
	require.Error(t, err)
	assert.True(t, errors.Is(err, tiled.ErrPropertyTypeConflict), "expected ErrPropertyTypeConflict, got: %v", err)
	assert.Contains(t, err.Error(), "health")
}

func TestEffectiveObjectInvalidProperty(t *testing.T) {
	const mapXML = `<?xml version="1.0" encoding="UTF-8"?>
<map version="1.10" orientation="orthogonal" renderorder="right-down" width="10" height="10" tilewidth="32" tileheight="32" infinite="0" nextlayerid="2" nextobjectid="2">
 <objectgroup id="1" name="Objects">
  <object id="1" template="test_template_invalid.tx" x="0" y="0"/>
 </objectgroup>
</map>`

	m, err := tiled.LoadReader("assets", strings.NewReader(mapXML))
	require.NoError(t, err)

	_, err = m.ObjectGroups[0].Objects[0].Effective()
	require.Error(t, err)
	assert.True(t, errors.Is(err, tiled.ErrInvalidProperty), "expected ErrInvalidProperty, got: %v", err)
	assert.Contains(t, err.Error(), "health")
}

func TestEffectiveObjectTemplateNotLoaded(t *testing.T) {
	obj := &tiled.Object{ID: 7, TemplateSource: "unloaded.tx"}
	_, err := obj.Effective()
	require.Error(t, err)
	assert.True(t, errors.Is(err, tiled.ErrTemplateNotLoaded), "expected ErrTemplateNotLoaded, got: %v", err)
}
