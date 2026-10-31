package screeny

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslatePatchParamsToPixletSchema(t *testing.T) {
	params := []StudioParam{
		{
			ID:      "sound",
			Label:   "Sound Enabled",
			Min:     0,
			Max:     1,
			Step:    1,
			Default: 1,
			Switch:  true,
		},
		{
			ID:      "style",
			Label:   "Clock Style",
			Min:     0,
			Max:     2,
			Step:    1,
			Default: 0,
			Choices: []string{"Classic", "Digital", "Minimal"},
			Switch:  false,
		},
		{
			ID:      "speed",
			Label:   "Playback Speed",
			Min:     0.5,
			Max:     3.0,
			Step:    0.5,
			Default: 1.0,
			Switch:  false,
		},
	}

	// Test with includeScaleFilter = true (2x displays)
	schemaBytes, err := TranslatePatchParamsToPixletSchema(params, true)
	require.NoError(t, err)

	var root PixletSchemaRoot
	err = json.Unmarshal(schemaBytes, &root)
	require.NoError(t, err)

	assert.Equal(t, "1", root.Version)
	require.Len(t, root.Schema, 4)

	// 1. Switch param -> onoff
	assert.Equal(t, "sound", root.Schema[0].ID)
	assert.Equal(t, "Sound Enabled", root.Schema[0].Name)
	assert.Equal(t, "onoff", root.Schema[0].Type)
	assert.Equal(t, "true", root.Schema[0].Default)

	// 2. Choices param -> dropdown
	assert.Equal(t, "style", root.Schema[1].ID)
	assert.Equal(t, "Clock Style", root.Schema[1].Name)
	assert.Equal(t, "dropdown", root.Schema[1].Type)
	assert.Equal(t, "0", root.Schema[1].Default)
	require.Len(t, root.Schema[1].Options, 3)
	assert.Equal(t, "0", root.Schema[1].Options[0].Value)
	assert.Equal(t, "Classic", root.Schema[1].Options[0].Display)
	assert.Equal(t, "1", root.Schema[1].Options[1].Value)
	assert.Equal(t, "Digital", root.Schema[1].Options[1].Display)

	// 3. Range param -> text
	assert.Equal(t, "speed", root.Schema[2].ID)
	assert.Equal(t, "Playback Speed", root.Schema[2].Name)
	assert.Equal(t, "text", root.Schema[2].Type)
	assert.Contains(t, root.Schema[2].Description, "Range: 0.5 - 3 (step 0.5)")
	assert.Equal(t, "1", root.Schema[2].Default)

	// 4. Scale filter param -> dropdown
	assert.Equal(t, "scale_filter", root.Schema[3].ID)
	assert.Equal(t, "dropdown", root.Schema[3].Type)
	assert.Equal(t, "default", root.Schema[3].Default)

	// Test with includeScaleFilter = false (standard 64x32 displays)
	schemaBytesNoScale, err := TranslatePatchParamsToPixletSchema(params, false)
	require.NoError(t, err)

	var rootNoScale PixletSchemaRoot
	err = json.Unmarshal(schemaBytesNoScale, &rootNoScale)
	require.NoError(t, err)

	assert.Equal(t, "1", rootNoScale.Version)
	require.Len(t, rootNoScale.Schema, 3)
	for _, field := range rootNoScale.Schema {
		assert.NotEqual(t, "scale_filter", field.ID)
	}
}
