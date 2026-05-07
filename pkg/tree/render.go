package tree

import (
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/srz-zumix/go-gh-extension/pkg/render"
)

// Renderer extends go-gh-extension's Renderer with tree-specific methods.
type Renderer struct {
	*render.Renderer
}

// NewRenderer creates a new Renderer with the provided exporter and default IOStreams.
func NewRenderer(ex cmdutil.Exporter) *Renderer {
	return &Renderer{render.NewRenderer(ex)}
}

type treeDirInfoFieldGetter func(d *TreeDirectoryInfo) string

func newTreeDirInfoFieldGetters() map[string]treeDirInfoFieldGetter {
	return map[string]treeDirInfoFieldGetter{
		"PATH": func(d *TreeDirectoryInfo) string {
			return d.Path
		},
		"DEPTH": func(d *TreeDirectoryInfo) string {
			return fmt.Sprintf("%d", d.Depth)
		},
		"ENTRY_COUNT": func(d *TreeDirectoryInfo) string {
			return humanize.Comma(int64(d.EntryCount))
		},
		"TOTAL_FILES": func(d *TreeDirectoryInfo) string {
			return humanize.Comma(int64(d.TotalFiles))
		},
		"EST_TREE_SIZE": func(d *TreeDirectoryInfo) string {
			return humanize.Bytes(uint64(d.EstTreeSize))
		},
	}
}

// RenderTreeDirectoryInfo renders a table of per-directory tree analysis results
// followed by an aggregate summary line.
// When an exporter is configured (e.g. --format json), the raw result is exported instead.
// If headers is nil the default column order PATH, DEPTH, ENTRY_COUNT, TOTAL_FILES, EST_TREE_SIZE is used.
func (r *Renderer) RenderTreeDirectoryInfo(result *TreeAnalysisResult, headers []string) error {
	if r.HasExporter() {
		return r.RenderExportedData(result)
	}

	if len(result.Dirs) == 0 {
		r.WriteLine("No directories found matching the threshold.")
		return nil
	}

	if len(headers) == 0 {
		headers = []string{"PATH", "DEPTH", "ENTRY_COUNT", "TOTAL_FILES", "EST_TREE_SIZE"}
	}

	getters := newTreeDirInfoFieldGetters()
	table := r.NewTableWriter(headers)
	for _, d := range result.Dirs {
		row := make([]string, len(headers))
		for i, h := range headers {
			h = strings.ToUpper(h)
			if getter, ok := getters[h]; ok {
				row[i] = getter(d)
			}
		}
		table.Append(row)
	}
	if err := table.Render(); err != nil {
		return err
	}

	r.WriteLine(fmt.Sprintf(
		"\nTotal: %s dirs, %s files, ~%s tree objects",
		humanize.Comma(int64(result.TotalDirs)),
		humanize.Comma(int64(result.TotalFiles)),
		humanize.Bytes(uint64(result.TotalEstTreeSize)),
	))
	r.WriteLine(fmt.Sprintf("Max depth: %d", result.MaxDepth))
	return nil
}
