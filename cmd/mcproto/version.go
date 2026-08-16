package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/go-theft-craft/minecraft-protocol/capture"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
)

const versionUsage = `mcproto version prints what this tool is and what it speaks.

Usage:
  mcproto version [--format text|json]

Examples:
  mcproto version
  mcproto version --format json
`

// versionReport is the machine-readable form. It is a struct rather than a map
// so the field set is fixed and a consumer can rely on it.
type versionReport struct {
	Module        string           `json:"module"`
	Revision      string           `json:"revision"`
	Go            string           `json:"go"`
	CaptureFormat int              `json:"captureFormat"`
	DigestVersion int              `json:"digestVersion"`
	Protocols     []protocolReport `json:"protocols"`
}

type protocolReport struct {
	ID       string `json:"id"`
	Edition  string `json:"edition"`
	Name     string `json:"name"`
	Protocol int32  `json:"protocol"`
}

func runVersion(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "text", "output format: text or json")

	if err := parseFlags(flags, args, versionUsage); err != nil {
		return err
	}

	report := buildVersionReport()

	switch *format {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")

		return encoder.Encode(report)
	case "text":
		_, _ = fmt.Fprintf(stdout, "module\t%s\n", report.Module)
		_, _ = fmt.Fprintf(stdout, "revision\t%s\n", report.Revision)
		_, _ = fmt.Fprintf(stdout, "go\t%s\n", report.Go)
		_, _ = fmt.Fprintf(stdout, "capture format\t%d\n", report.CaptureFormat)
		for _, entry := range report.Protocols {
			_, _ = fmt.Fprintf(stdout, "protocol\t%s\t%s\t%d\n", entry.ID, entry.Name, entry.Protocol)
		}

		return nil
	default:
		return usagef("unknown format %q; use text or json\n\n%s", *format, versionUsage)
	}
}

func buildVersionReport() versionReport {
	report := versionReport{
		Module:        "github.com/go-theft-craft/minecraft-protocol",
		Revision:      "unknown",
		Go:            "unknown",
		CaptureFormat: capture.FormatVersion,
		DigestVersion: capture.DigestVersion,
	}

	// Build information is absent in `go test` binaries and in some build
	// modes, which is why the fields have a stated default rather than being
	// left empty for a consumer to guess at.
	if info, ok := debug.ReadBuildInfo(); ok {
		report.Go = info.GoVersion
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				report.Revision = setting.Value
			}
		}
	}

	for _, descriptor := range protocols.All() {
		version := descriptor.Version()
		report.Protocols = append(report.Protocols, protocolReport{
			ID:       descriptor.ID(),
			Edition:  string(descriptor.Edition()),
			Name:     version.Name,
			Protocol: version.Protocol,
		})
	}

	return report
}
