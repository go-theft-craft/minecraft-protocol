// Command mcdata-gen validates source Minecraft data and generates game data and packet codecs.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/generator"
)

func main() {
	source := flag.String("source", "", "source data directory")
	output := flag.String("out", "", "output base directory")
	packageName := flag.String("package", "", "generated Go package name")
	version := flag.String("version", "", "stable registration key (for example java/1.8.9)")
	check := flag.Bool("check", false, "check generated data and packet codecs without changing them")
	coverage := flag.Bool("coverage", false, "also generate coverage.json: every packet the compiler produced a codec for")
	raw := flag.Bool("raw", false, "also generate the raw dataset set: every source dataset as the bytes upstream published")
	flag.Parse()

	missing := false
	for _, required := range []struct {
		name  string
		value string
	}{
		{name: "source", value: *source},
		{name: "out", value: *output},
		{name: "package", value: *packageName},
		{name: "version", value: *version},
	} {
		if required.value == "" {
			fmt.Fprintf(os.Stderr, "error: -%s is required\n", required.name)
			missing = true
		}
	}
	if missing {
		flag.Usage()
		os.Exit(2)
	}

	config := generator.Config{SourceDir: *source, OutDir: *output, Package: *packageName, Version: *version, IncludeCoverage: *coverage, IncludeRaw: *raw}
	operation := generator.Run
	if *check {
		operation = generator.Check
	}
	if err := operation(config); err != nil {
		fmt.Fprintf(os.Stderr, "mcdata-gen: %v\n", err)
		os.Exit(1)
	}

	verb := "generated"
	if *check {
		verb = "checked"
	}
	fmt.Printf("mcdata-gen: %s %s -> %s\n", verb, relativePath(*source), relativePath(filepath.Join(*output, *packageName)))
}

func relativePath(path string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return filepath.Base(path)
	}
	relative, err := filepath.Rel(workingDirectory, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(relative)
}
