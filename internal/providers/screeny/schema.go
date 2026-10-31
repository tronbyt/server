package screeny

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// StudioParam matches screeny-studio's ParamInfo JSON definition.
type StudioParam struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Min     float32  `json:"min"`
	Max     float32  `json:"max"`
	Step    float32  `json:"step"`
	Default float32  `json:"default"`
	Choices []string `json:"choices"`
	Switch  bool     `json:"switch"`
}

// StudioPatch matches screeny-studio's PatchInfo JSON definition from GET /api/v1/bootstrap.
type StudioPatch struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Blurb            string        `json:"blurb"`
	Params           []StudioParam `json:"params"`
	NeedsGPU         bool          `json:"needs_gpu"`
	Playable         bool          `json:"playable"`
	UnplayableReason *string       `json:"unplayable_reason,omitempty"`
	Seeded           bool          `json:"seeded"`
}

// StudioBootstrap matches screeny-studio's GET /api/v1/bootstrap response.
type StudioBootstrap struct {
	Patches []StudioPatch `json:"patches"`
}

// PixletSchemaField represents an individual field inside Tronbyt's Pixlet schema.
type PixletSchemaField struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon,omitempty"`
	Type        string                 `json:"type"`
	Default     string                 `json:"default,omitempty"`
	Options     []PixletDropdownOption `json:"options,omitempty"`
}

// PixletDropdownOption represents an option item in a Pixlet dropdown field.
type PixletDropdownOption struct {
	Value   string `json:"value"`
	Display string `json:"display"`
}

// PixletSchemaRoot represents the root schema JSON object consumed by Tronbyt web UI.
type PixletSchemaRoot struct {
	Version string              `json:"version"`
	Schema  []PixletSchemaField `json:"schema"`
}

// TranslatePatchParamsToPixletSchema converts a list of Screeny Studio parameters into
// a Pixlet-compatible JSON schema string. When includeScaleFilter is true, a 128x64 upscaling
// filter dropdown is included (only for devices that support 2x / 128x64 scaling).
func TranslatePatchParamsToPixletSchema(params []StudioParam, includeScaleFilter bool) ([]byte, error) {
	fields := make([]PixletSchemaField, 0, len(params)+1)

	for _, p := range params {
		field := PixletSchemaField{
			ID:          p.ID,
			Name:        p.Label,
			Description: "",
		}

		switch {
		case p.Switch:
			field.Type = "onoff"
			field.Icon = "toggleOn"
			if p.Default > 0.5 {
				field.Default = "true"
			} else {
				field.Default = "false"
			}

		case len(p.Choices) > 0:
			field.Type = "dropdown"
			field.Icon = "list"
			options := make([]PixletDropdownOption, 0, len(p.Choices))
			for i, choice := range p.Choices {
				val := strconv.Itoa(i)
				options = append(options, PixletDropdownOption{
					Value:   val,
					Display: choice,
				})
			}
			field.Options = options
			defaultIdx := int(p.Default)
			if defaultIdx >= 0 && defaultIdx < len(p.Choices) {
				field.Default = strconv.Itoa(defaultIdx)
			} else {
				field.Default = "0"
			}

		default:
			field.Type = "text"
			field.Icon = "sliders"
			field.Description = fmt.Sprintf("Range: %g - %g (step %g)", p.Min, p.Max, p.Step)
			field.Default = fmt.Sprintf("%g", p.Default)
		}

		fields = append(fields, field)
	}

	// Add 128x64 upscaling filter option for 2x displays (e.g. Tronberry).
	if includeScaleFilter {
		fields = append(fields, PixletSchemaField{
			ID:          "scale_filter",
			Name:        "128x64 Upscaling Filter",
			Description: "Upscaling algorithm when displaying on 128x64 panels (like Tronberry)",
			Icon:        "expand",
			Type:        "dropdown",
			Default:     "default",
			Options: []PixletDropdownOption{
				{Value: "default", Display: "Server Default"},
				{Value: FilterBicubic, Display: "Bicubic (Smooth 128x64 Gradients & 3D Shaders)"},
				{Value: FilterScale2x, Display: "Scale2x / EPX (Sharp 128x64 Pixel Art & Clocks)"},
				{Value: FilterBilinear, Display: "Bilinear (Soft)"},
				{Value: FilterNearest, Display: "Nearest (Retro 2x2 Blocks)"},
			},
		})
	}

	root := PixletSchemaRoot{
		Version: "1",
		Schema:  fields,
	}

	return json.Marshal(root)
}
