package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-theft-craft/minecraft-protocol/internal/sourcefetch"
	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

const dataUsage = `mcproto data manages pinned upstream data trees.

Usage:
  mcproto data fetch    --edition java --version 26.1 --protocol 775 \
                        --revision <full commit> --output source/java/26.1
  mcproto data validate --source source/java/26.1 [--format json]

fetch downloads every dataset upstream lists for a version, records each one's
origin and checksum, and replaces the output directory only once all of it
arrived. A failed fetch leaves the previous tree in place.

validate re-reads a tree and checks it against its own manifest.
`

func runData(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usagef("data needs a subcommand\n\n%s", dataUsage)
	}

	switch args[0] {
	case "fetch":
		return runDataFetch(ctx, args[1:], stdout)
	case "validate":
		return runDataValidate(args[1:], stdout)
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, dataUsage)

		return nil
	default:
		return usagef("unknown data subcommand %q\n\n%s", args[0], dataUsage)
	}
}

// datasetReport is one dataset in machine-readable output.
type datasetReport struct {
	Name          string `json:"name"`
	File          string `json:"file"`
	SourcePath    string `json:"sourcePath"`
	SourceVersion string `json:"sourceVersion"`
	Aliased       bool   `json:"aliased"`
	SHA256        string `json:"sha256"`
}

// treeReport is what fetch and validate print with --format json. It is the
// stable surface: a caller reads it instead of parsing prose.
type treeReport struct {
	Edition                string          `json:"edition"`
	TargetMinecraftVersion string          `json:"targetMinecraftVersion"`
	SourceMinecraftVersion string          `json:"sourceMinecraftVersion"`
	Protocol               int32           `json:"protocol"`
	SourceRevision         string          `json:"sourceRevision"`
	DatasetCount           int             `json:"datasetCount"`
	AliasedCount           int             `json:"aliasedCount"`
	Datasets               []datasetReport `json:"datasets"`
	// Measured names the datasets read out of a Mojang jar rather than
	// fetched from upstream. They verify the same way and come from somewhere
	// else, and a reader auditing the tree has to be able to tell which is
	// which without opening the manifest.
	Measured []string `json:"measured,omitempty"`
}

func newTreeReport(loaded *manifest.Manifest) treeReport {
	report := treeReport{
		Edition:                loaded.Edition,
		TargetMinecraftVersion: loaded.TargetMinecraftVersion,
		SourceMinecraftVersion: loaded.SourceMinecraftVersion,
		Protocol:               loaded.Protocol,
		SourceRevision:         loaded.SourceRevision,
		DatasetCount:           len(loaded.Datasets),
		Datasets:               make([]datasetReport, 0, len(loaded.Datasets)),
	}
	if loaded.Extracted != nil {
		for _, dataset := range loaded.Extracted.Datasets {
			report.Measured = append(report.Measured, dataset.Name)
		}
		sort.Strings(report.Measured)
	}

	for _, dataset := range loaded.Datasets {
		aliased := dataset.Aliased(loaded.SourceVersionDirectory)
		if aliased {
			report.AliasedCount++
		}
		report.Datasets = append(report.Datasets, datasetReport{
			Name:          dataset.Name,
			File:          dataset.File,
			SourcePath:    dataset.SourcePath,
			SourceVersion: dataset.SourceVersion(),
			Aliased:       aliased,
			SHA256:        dataset.SHA256,
		})
	}
	sort.Slice(report.Datasets, func(i, j int) bool {
		return report.Datasets[i].Name < report.Datasets[j].Name
	})

	return report
}

// checkFormat rejects an unknown format before any work runs, so a mistyped
// flag costs nothing and reports a usage error rather than a runtime one.
func checkFormat(format string) error {
	if format != "text" && format != "json" {
		return usagef("unknown format %q, want text or json", format)
	}

	return nil
}

func (r treeReport) write(stdout io.Writer, format string) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")

		return encoder.Encode(r)
	case "text":
		_, _ = fmt.Fprintf(stdout, "%s/%s protocol %d at %s\n", r.Edition, r.TargetMinecraftVersion, r.Protocol, r.SourceRevision)
		_, _ = fmt.Fprintf(stdout, "%d datasets, %d resolved from an older version\n", r.DatasetCount, r.AliasedCount)
		if len(r.Measured) > 0 {
			_, _ = fmt.Fprintf(stdout, "%d measured from a game jar: %s\n", len(r.Measured), strings.Join(r.Measured, ", "))
		}
		for _, dataset := range r.Datasets {
			if dataset.Aliased {
				_, _ = fmt.Fprintf(stdout, "  %-22s %s (alias of %s)\n", dataset.Name, dataset.SourcePath, dataset.SourceVersion)
			}
		}

		return nil
	default:
		return usagef("unknown format %q, want text or json", format)
	}
}

func runDataFetch(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("mcproto data fetch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	edition := flags.String("edition", "java", "edition to fetch")
	version := flags.String("version", "", "upstream data version, such as 26.1")
	protocol := flags.Int("protocol", 0, "protocol number the data must declare")
	revision := flags.String("revision", "", "full upstream commit to pin")
	output := flags.String("output", "", "directory to replace with the fetched tree")
	format := flags.String("format", "text", "output format: text or json")

	if err := flags.Parse(args); err != nil {
		return usagef("%w\n\n%s", err, dataUsage)
	}
	if flags.NArg() > 0 {
		return usagef("unexpected argument %q", flags.Arg(0))
	}
	if err := checkFormat(*format); err != nil {
		return err
	}

	written, err := sourcefetch.Fetch(ctx, sourcefetch.Options{
		Edition:   *edition,
		Version:   *version,
		Protocol:  int32(*protocol),
		Revision:  *revision,
		OutputDir: *output,
	})
	if err != nil {
		return err
	}

	return newTreeReport(written).write(stdout, *format)
}

func runDataValidate(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("mcproto data validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	source := flags.String("source", "", "source tree to validate")
	format := flags.String("format", "text", "output format: text or json")

	if err := flags.Parse(args); err != nil {
		return usagef("%w\n\n%s", err, dataUsage)
	}
	if flags.NArg() > 0 {
		return usagef("unexpected argument %q", flags.Arg(0))
	}
	if *source == "" {
		return usagef("--source is required")
	}
	if err := checkFormat(*format); err != nil {
		return err
	}

	loaded, err := manifest.Load(*source)
	if err != nil {
		return err
	}
	if err := loaded.Verify(*source); err != nil {
		return err
	}

	return newTreeReport(loaded).write(stdout, *format)
}
