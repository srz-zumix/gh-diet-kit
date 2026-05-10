package dangling

import (
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/srz-zumix/go-gh-extension/pkg/render"
)

// Renderer extends go-gh-extension's Renderer with dangling-specific methods.
// All non-dangling methods are promoted through embedding.
type Renderer struct {
	*render.Renderer
}

// NewRenderer creates a new Renderer with the provided exporter and default IOStreams.
func NewRenderer(ex cmdutil.Exporter) *Renderer {
	return &Renderer{render.NewRenderer(ex)}
}

type danglingCommitFieldGetter func(c *DanglingCommit) string
type danglingCommitFieldGetters struct {
	Func map[string]danglingCommitFieldGetter
}

func newDanglingCommitFieldGetters() *danglingCommitFieldGetters {
	return &danglingCommitFieldGetters{
		Func: map[string]danglingCommitFieldGetter{
			"SHA": func(c *DanglingCommit) string {
				return c.SHA
			},
			"PR_NUMBER": func(c *DanglingCommit) string {
				return fmt.Sprintf("%d", c.PRNumber)
			},
			"PR_URL": func(c *DanglingCommit) string {
				return c.PRURL
			},
			"SIZE": func(c *DanglingCommit) string {
				if c.TotalBlobSize == nil {
					return ""
				}
				return humanize.Bytes(*c.TotalBlobSize)
			},
			"MESSAGE": func(c *DanglingCommit) string {
				return firstLineOf(c.Message)
			},
		},
	}
}

func (g *danglingCommitFieldGetters) getField(c *DanglingCommit, field string) string {
	field = strings.ToUpper(field)
	if getter, ok := g.Func[field]; ok {
		return getter(c)
	}
	return ""
}

// RenderDanglingCommits renders a table of dangling commits with the specified headers.
// When an exporter is configured (e.g. --format json), the raw slice is exported instead.
func (r *Renderer) RenderDanglingCommits(commits []*DanglingCommit, headers []string) error {
	if r.HasExporter() {
		return r.RenderExportedData(commits)
	}

	if len(commits) == 0 {
		r.WriteLine("No dangling commits found.")
		return nil
	}

	if len(headers) == 0 {
		headers = []string{"SHA", "PR_NUMBER", "PR_URL", "SIZE", "MESSAGE"}
	}

	getter := newDanglingCommitFieldGetters()
	table := r.NewTableWriter(headers)
	for _, c := range commits {
		row := make([]string, len(headers))
		for i, header := range headers {
			row[i] = getter.getField(c, header)
		}
		table.Append(row)
	}
	return table.Render()
}

type danglingBlobFieldGetter func(b *DanglingBlob) string
type danglingBlobFieldGetters struct {
	Func map[string]danglingBlobFieldGetter
}

func newDanglingBlobFieldGetters() *danglingBlobFieldGetters {
	return &danglingBlobFieldGetters{
		Func: map[string]danglingBlobFieldGetter{
			"SHA": func(b *DanglingBlob) string {
				return b.SHA
			},
			"PATH": func(b *DanglingBlob) string {
				return b.Path
			},
			"SIZE": func(b *DanglingBlob) string {
				if b.Size == nil {
					return ""
				}
				return humanize.Bytes(uint64(*b.Size))
			},
			"COMMIT_SHA": func(b *DanglingBlob) string {
				return b.CommitSHA
			},
			"PR_NUMBER": func(b *DanglingBlob) string {
				return fmt.Sprintf("%d", b.PRNumber)
			},
			"PR_URL": func(b *DanglingBlob) string {
				return b.PRURL
			},
		},
	}
}

func (g *danglingBlobFieldGetters) getField(b *DanglingBlob, field string) string {
	field = strings.ToUpper(field)
	if getter, ok := g.Func[field]; ok {
		return getter(b)
	}
	return ""
}

// RenderDanglingBlobs renders a table of dangling blobs with the specified headers.
// When an exporter is configured (e.g. --format json), the raw slice is exported instead.
func (r *Renderer) RenderDanglingBlobs(blobs []*DanglingBlob, headers []string) error {
	if r.HasExporter() {
		return r.RenderExportedData(blobs)
	}

	if len(blobs) == 0 {
		r.WriteLine("No dangling blobs found.")
		return nil
	}

	if len(headers) == 0 {
		headers = []string{"SHA", "PATH", "SIZE", "COMMIT_SHA", "PR_NUMBER", "PR_URL"}
	}

	getter := newDanglingBlobFieldGetters()
	table := r.NewTableWriter(headers)
	for _, b := range blobs {
		row := make([]string, len(headers))
		for i, header := range headers {
			row[i] = getter.getField(b, header)
		}
		table.Append(row)
	}
	return table.Render()
}

type noPRBranchFieldGetter func(b *NoPRBranch) string
type noPRBranchFieldGetters struct {
	Func map[string]noPRBranchFieldGetter
}

func newNoPRBranchFieldGetters() *noPRBranchFieldGetters {
	return &noPRBranchFieldGetters{
		Func: map[string]noPRBranchFieldGetter{
			"BRANCH": func(b *NoPRBranch) string {
				return b.Name
			},
			"COMMIT_SHA": func(b *NoPRBranch) string {
				return b.CommitSHA
			},
			"AHEAD_COUNT": func(b *NoPRBranch) string {
				if b.AheadCount < 0 {
					return ""
				}
				return fmt.Sprintf("%d", b.AheadCount)
			},
			"AUTHOR": func(b *NoPRBranch) string {
				return b.Author
			},
			"UNIQUE_SIZE": func(b *NoPRBranch) string {
				if b.UniqueBlobSize == nil {
					return ""
				}
				return humanize.Bytes(*b.UniqueBlobSize)
			},
		},
	}
}

func (g *noPRBranchFieldGetters) getField(b *NoPRBranch, field string) string {
	field = strings.ToUpper(field)
	if getter, ok := g.Func[field]; ok {
		return getter(b)
	}
	return ""
}

// RenderNoPRBranches renders a table of branches without pull requests.
// When an exporter is configured (e.g. --format json), the raw slice is exported instead.
func (r *Renderer) RenderNoPRBranches(branches []*NoPRBranch, headers []string) error {
	if r.HasExporter() {
		return r.RenderExportedData(branches)
	}

	if len(branches) == 0 {
		r.WriteLine("No branches without pull requests found.")
		return nil
	}

	if len(headers) == 0 {
		headers = []string{"BRANCH", "COMMIT_SHA", "AHEAD_COUNT", "AUTHOR", "UNIQUE_SIZE"}
	}

	getter := newNoPRBranchFieldGetters()
	table := r.NewTableWriter(headers)
	for _, b := range branches {
		row := make([]string, len(headers))
		for i, header := range headers {
			row[i] = getter.getField(b, header)
		}
		table.Append(row)
	}
	return table.Render()
}

// firstLineOf returns the first line of a potentially multi-line string.
func firstLineOf(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
