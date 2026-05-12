package lfs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/dustin/go-humanize"
	"github.com/srz-zumix/go-gh-extension/pkg/render"
)

// SARIF 2.1.0 minimal types for LFS candidate output.

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	ShortDescription sarifMessage       `json:"shortDescription"`
	DefaultConfig    sarifDefaultConfig `json:"defaultConfiguration"`
}

type sarifDefaultConfig struct {
	Level string `json:"level"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
}

type sarifArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
}

// Renderer extends go-gh-extension's Renderer with LFS-specific methods.
type Renderer struct {
	*render.Renderer
}

// NewRenderer creates a new Renderer with the provided exporter and default IOStreams.
func NewRenderer(ex cmdutil.Exporter) *Renderer {
	return &Renderer{render.NewRenderer(ex)}
}

type lfsCandidateFieldGetter func(c *LFSCandidate) string

func newLFSCandidateFieldGetters() map[string]lfsCandidateFieldGetter {
	return map[string]lfsCandidateFieldGetter{
		"SHA": func(c *LFSCandidate) string {
			return c.SHA
		},
		"PATH": func(c *LFSCandidate) string {
			return c.Path
		},
		"SIZE": func(c *LFSCandidate) string {
			return humanize.Bytes(c.Size)
		},
	}
}

// RenderLFSCandidates renders a table of LFS candidate blobs.
// When an exporter is configured (e.g. --format json), the raw slice is exported instead.
// If headers is nil the default column order PATH, SIZE, SHA is used.
func (r *Renderer) RenderLFSCandidates(candidates []*LFSCandidate, headers []string) error {
	if r.HasExporter() {
		return r.RenderExportedData(candidates)
	}

	if len(candidates) == 0 {
		r.WriteLine("No LFS candidates found.")
		return nil
	}

	if len(headers) == 0 {
		headers = []string{"PATH", "SIZE", "SHA"}
	}

	getters := newLFSCandidateFieldGetters()
	table := r.NewTableWriter(headers)
	for _, c := range candidates {
		row := make([]string, len(headers))
		for i, h := range headers {
			h = strings.ToUpper(h)
			if getter, ok := getters[h]; ok {
				row[i] = getter(c)
			}
		}
		table.Append(row)
	}
	return table.Render()
}

// RenderLFSCandidatesAsSARIF writes the LFS candidates as a SARIF 2.1.0 document to stdout.
func (r *Renderer) RenderLFSCandidatesAsSARIF(candidates []*LFSCandidate) error {
	const ruleID = "lfs-candidate"

	results := make([]sarifResult, 0, len(candidates))
	for _, c := range candidates {
		results = append(results, sarifResult{
			RuleID: ruleID,
			Level:  "warning",
			Message: sarifMessage{
				Text: fmt.Sprintf("File '%s' (%s) should be tracked by Git LFS", c.Path, humanize.Bytes(c.Size)),
			},
			Locations: []sarifLocation{
				{
					PhysicalLocation: sarifPhysicalLocation{
						ArtifactLocation: sarifArtifactLocation{
							URI:       c.Path,
							URIBaseID: "%SRCROOT%",
						},
					},
				},
			},
		})
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{
			{
				Tool: sarifTool{
					Driver: sarifDriver{
						Name: "gh-diet-kit",
						Rules: []sarifRule{
							{
								ID:               ruleID,
								Name:             "LFSCandidate",
								ShortDescription: sarifMessage{Text: "File should be tracked by Git LFS"},
								DefaultConfig:    sarifDefaultConfig{Level: "warning"},
							},
						},
					},
				},
				Results: results,
			},
		},
	}

	enc := json.NewEncoder(r.IO.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

type lfsEstimateFieldGetter func(e *LFSSavingEstimate) string

func newLFSEstimateFieldGetters() map[string]lfsEstimateFieldGetter {
	return map[string]lfsEstimateFieldGetter{
		"PATH": func(e *LFSSavingEstimate) string {
			return e.Path
		},
		"SHA": func(e *LFSSavingEstimate) string {
			return e.SHA
		},
		"CURRENT_SIZE": func(e *LFSSavingEstimate) string {
			return humanize.Bytes(uint64(e.CurrentSize))
		},
		"VERSIONS": func(e *LFSSavingEstimate) string {
			return humanize.Comma(int64(e.VersionCount))
		},
		"ESTIMATED_TOTAL_SIZE": func(e *LFSSavingEstimate) string {
			return humanize.Bytes(e.EstimatedTotalSize)
		},
		"ESTIMATED_SAVING": func(e *LFSSavingEstimate) string {
			return humanize.Bytes(e.EstimatedSaving)
		},
	}
}

// RenderLFSSavingEstimates renders a table of per-file migration saving estimates
// followed by an aggregate summary line.
// When an exporter is configured (e.g. --format json), the raw slices are exported instead.
func (r *Renderer) RenderLFSSavingEstimates(estimates []*LFSSavingEstimate, summary *LFSMigrationSummary, headers []string) error {
	if r.HasExporter() {
		type exportedResult struct {
			Estimates []*LFSSavingEstimate `json:"estimates"`
			Summary   *LFSMigrationSummary `json:"summary"`
		}
		return r.RenderExportedData(&exportedResult{Estimates: estimates, Summary: summary})
	}

	if len(estimates) == 0 {
		r.WriteLine("No LFS candidates found.")
		return nil
	}

	if len(headers) == 0 {
		if summary != nil && summary.HistoryScanned {
			headers = []string{"PATH", "CURRENT_SIZE", "VERSIONS", "ESTIMATED_TOTAL_SIZE", "ESTIMATED_SAVING"}
		} else {
			headers = []string{"PATH", "CURRENT_SIZE", "ESTIMATED_SAVING"}
		}
	}

	getters := newLFSEstimateFieldGetters()
	table := r.NewTableWriter(headers)
	for _, e := range estimates {
		row := make([]string, len(headers))
		for i, h := range headers {
			h = strings.ToUpper(h)
			if getter, ok := getters[h]; ok {
				row[i] = getter(e)
			}
		}
		table.Append(row)
	}
	if err := table.Render(); err != nil {
		return err
	}

	if summary != nil {
		r.WriteLine("")
		r.WriteLine("Summary:")
		r.WriteLine("  Candidates:       " + humanize.Comma(int64(summary.CandidateCount)))
		r.WriteLine("  Current size:     " + humanize.Bytes(summary.TotalCurrentSize))
		if summary.HistoryScanned {
			r.WriteLine("  Est. total size:  " + humanize.Bytes(summary.TotalEstimatedSize))
		}
		r.WriteLine("  Est. git saving:  " + humanize.Bytes(summary.TotalEstimatedSaving))
	}
	return nil
}
