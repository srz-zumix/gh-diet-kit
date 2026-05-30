package assets

import (
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/srz-zumix/go-gh-extension/pkg/render"
)

// Renderer extends go-gh-extension's Renderer with PR-asset-specific methods.
type Renderer struct {
	*render.Renderer
}

// NewRenderer creates a new Renderer with the provided exporter and default IOStreams.
func NewRenderer(ex cmdutil.Exporter) *Renderer {
	return &Renderer{render.NewRenderer(ex)}
}

type prAssetFieldGetter func(a *PRAsset) string

func newPRAssetFieldGetters() map[string]prAssetFieldGetter {
	return map[string]prAssetFieldGetter{
		"PR_NUMBER": func(a *PRAsset) string {
			return fmt.Sprintf("%d", a.PRNumber)
		},
		"PR_URL": func(a *PRAsset) string {
			return a.PRURL
		},
		"LOCATION": func(a *PRAsset) string {
			return string(a.Location)
		},
		"LOCATION_ID": func(a *PRAsset) string {
			if a.LocationID == 0 {
				return ""
			}
			return fmt.Sprintf("%d", a.LocationID)
		},
		"TYPE": func(a *PRAsset) string {
			return string(a.Type)
		},
		"FILENAME": func(a *PRAsset) string {
			return a.Filename
		},
		"FILE_SIZE": func(a *PRAsset) string {
			if a.FileSize < 0 {
				return "N/A"
			}
			return humanize.Bytes(uint64(a.FileSize))
		},
		"ASSET_URL": func(a *PRAsset) string {
			return a.AssetURL
		},
	}
}

// DefaultPRAssetHeaders returns the default column order for the asset table.
var DefaultPRAssetHeaders = []string{
	"PR_NUMBER", "LOCATION", "LOCATION_ID", "TYPE", "FILENAME", "FILE_SIZE", "ASSET_URL",
}

// RenderPRAssets renders a table of PR assets.
// When an exporter is configured (e.g. --format json), the raw slice is exported instead.
// If headers is nil the default column order is used.
func (r *Renderer) RenderPRAssets(assets []*PRAsset, headers []string) error {
	if r.HasExporter() {
		return r.RenderExportedData(assets)
	}

	if len(assets) == 0 {
		r.WriteLine("No PR assets found.")
		return nil
	}

	if len(headers) == 0 {
		headers = DefaultPRAssetHeaders
	}

	getters := newPRAssetFieldGetters()
	table := r.NewTableWriter(headers)
	for _, a := range assets {
		row := make([]string, len(headers))
		for i, h := range headers {
			h = strings.ToUpper(h)
			if getter, ok := getters[h]; ok {
				row[i] = getter(a)
			}
		}
		table.Append(row)
	}
	return table.Render()
}
