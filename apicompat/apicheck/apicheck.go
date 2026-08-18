// Package apicheck freezes the parent module's public surface.
//
// The baseline under ../api is per-package gc export data plus a manifest,
// written by cmd/apibaseline (`task api:accept`) and compared by this
// package's test (`task api:check`) through golang.org/x/exp/apidiff. An
// incompatible change to an exported symbol fails the check and is either
// reverted or accepted deliberately by rewriting the baseline in the same
// commit, where a reviewer sees both halves at once. A compatible change is
// news, not a failure.
//
// This lives in a nested module rather than in the parent's internal tree
// because the parent's go.mod is dependency-free and stays that way: the
// tooling needs apidiff and its loader, and a consumer of the protocol module
// should not see either. The same shape keeps the examples modules' Prometheus
// client out of the server's dependency list.
package apicheck

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/exp/apidiff"
	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/go/packages"
)

// manifestName is the human half of the baseline: which packages are frozen.
const manifestName = "api.txt"

// Load type-checks every exported package of the module rooted at dir.
//
// Internal packages and main packages are left out: nothing outside the
// module can import the first, and nothing at all imports the second. The
// generated packages stay in, deliberately — consumers import them directly,
// and a regeneration that changes a field type is exactly the change a
// baseline is for.
func Load(dir string) (map[string]*types.Package, *token.FileSet, error) {
	fset := token.NewFileSet()
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedDeps |
			packages.NeedImports | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:  dir,
		Fset: fset,
	}, "./...")
	if err != nil {
		return nil, nil, fmt.Errorf("load %s: %w", dir, err)
	}

	surface := make(map[string]*types.Package)
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, nil, fmt.Errorf("load %s: %v", pkg.PkgPath, pkg.Errors[0])
		}
		if pkg.Name == "main" || strings.Contains(pkg.PkgPath+"/", "/internal/") {
			continue
		}
		surface[pkg.PkgPath] = pkg.Types
	}
	if len(surface) == 0 {
		return nil, nil, fmt.Errorf("load %s: no exported packages", dir)
	}

	return surface, fset, nil
}

// WriteBaseline records the surface under baselineDir: one export-data file
// per package, and the manifest naming them.
func WriteBaseline(baselineDir string, surface map[string]*types.Package, fset *token.FileSet) error {
	if err := os.MkdirAll(baselineDir, 0o755); err != nil {
		return err
	}

	paths := sortedPaths(surface)
	var manifest strings.Builder
	manifest.WriteString("# The frozen public surface of " + moduleOf(paths) + ".\n")
	manifest.WriteString("# One export-data file per package; task api:check compares them\n")
	manifest.WriteString("# through apidiff and task api:accept rewrites them on purpose.\n")
	for _, path := range paths {
		file, err := os.Create(filepath.Join(baselineDir, slug(path)+".export"))
		if err != nil {
			return err
		}
		if err := gcexportdata.Write(file, fset, surface[path]); err != nil {
			_ = file.Close()

			return fmt.Errorf("export %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return err
		}
		manifest.WriteString(path + " " + slug(path) + ".export\n")
	}

	return os.WriteFile(filepath.Join(baselineDir, manifestName), []byte(manifest.String()), 0o644)
}

// ReadBaseline loads the recorded surface back.
func ReadBaseline(baselineDir string) (map[string]*types.Package, error) {
	content, err := os.ReadFile(filepath.Join(baselineDir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("no baseline: %w (run task api:accept to record one)", err)
	}

	fset := token.NewFileSet()
	imports := make(map[string]*types.Package)
	baseline := make(map[string]*types.Package)
	for _, line := range strings.Split(string(content), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, file, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("baseline manifest line %q", line)
		}
		reader, err := os.Open(filepath.Join(baselineDir, file))
		if err != nil {
			return nil, err
		}
		pkg, err := gcexportdata.Read(reader, fset, imports, path)
		_ = reader.Close()
		if err != nil {
			return nil, fmt.Errorf("read baseline for %s: %w", path, err)
		}
		baseline[path] = pkg
	}

	return baseline, nil
}

// Result is one comparison's findings, incompatible and compatible apart:
// the first is a failure, the second is news.
type Result struct {
	Incompatible []string
	Compatible   []string
}

// Compare diffs the current surface against the baseline.
//
// A package in the baseline and not in the current surface is itself an
// incompatible change — a consumer imported it — and a new package is
// compatible news.
func Compare(baseline, current map[string]*types.Package) Result {
	var result Result
	for _, path := range sortedPaths(baseline) {
		now, ok := current[path]
		if !ok {
			result.Incompatible = append(result.Incompatible, path+": package removed")

			continue
		}
		report := apidiff.Changes(baseline[path], now)
		for _, change := range report.Changes {
			line := path + ": " + change.Message
			if change.Compatible {
				result.Compatible = append(result.Compatible, line)
			} else {
				result.Incompatible = append(result.Incompatible, line)
			}
		}
	}
	for _, path := range sortedPaths(current) {
		if _, ok := baseline[path]; !ok {
			result.Compatible = append(result.Compatible, path+": package added")
		}
	}

	return result
}

// slug renders an import path as a file name.
func slug(path string) string { return strings.ReplaceAll(path, "/", "_") }

// sortedPaths keeps every walk deterministic.
func sortedPaths(surface map[string]*types.Package) []string {
	paths := make([]string, 0, len(surface))
	for path := range surface {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	return paths
}

// moduleOf names the module from its shortest package path.
func moduleOf(paths []string) string {
	if len(paths) == 0 {
		return "the module"
	}

	return paths[0]
}
