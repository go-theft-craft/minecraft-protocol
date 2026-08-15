package sourcefetch

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// maxAliasHops bounds alias following. Upstream resolves a dataset to a
// directory in one hop today; the bound exists so a cycle upstream is an error
// here rather than a hang.
const maxAliasHops = 8

// upstreamEdition translates a manifest edition into the name upstream uses.
// PrismarineJS calls the Java edition "pc", and every directory it names is
// prefixed the same way, so the translation has to happen once at the boundary
// rather than being spelled out at each use.
func upstreamEdition(edition string) (string, error) {
	if edition != "java" {
		return "", fmt.Errorf("unsupported edition %q", edition)
	}

	return "pc", nil
}

// dataPaths is upstream's dataPaths.json: edition, then version, then dataset
// name to the directory that actually holds it. A version whose entry points
// at another directory is upstream reusing older data, which is the normal
// case for datasets that have not changed in years.
type dataPaths map[string]map[string]map[string]string

func parseDataPaths(raw []byte) (dataPaths, error) {
	var paths dataPaths
	if err := json.Unmarshal(raw, &paths); err != nil {
		return nil, fmt.Errorf("parse dataPaths.json: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("dataPaths.json names no editions")
	}

	return paths, nil
}

// resolved is one dataset's origin: the upstream directory holding it, in
// dataPaths form ("pc/1.16.1").
type resolved struct {
	Dataset   string
	Directory string
}

// resolve returns every dataset upstream lists for edition and version, sorted
// by dataset name so a fetch writes the same manifest every time.
//
// A directory that points somewhere else is followed, because upstream is free
// to alias an alias. Following stops when a directory maps the dataset to
// itself, which is how a concrete entry terminates.
func (p dataPaths) resolve(edition, version string) ([]resolved, error) {
	versions, ok := p[edition]
	if !ok {
		return nil, fmt.Errorf("dataPaths.json has no edition %q", edition)
	}
	entry, ok := versions[version]
	if !ok {
		return nil, fmt.Errorf("dataPaths.json has no %s version %q", edition, version)
	}
	if len(entry) == 0 {
		return nil, fmt.Errorf("%s/%s lists no datasets", edition, version)
	}

	names := make([]string, 0, len(entry))
	for name := range entry {
		names = append(names, name)
	}
	sort.Strings(names)

	all := make([]resolved, 0, len(names))
	for _, name := range names {
		directory, err := p.follow(edition, version, name)
		if err != nil {
			return nil, err
		}
		all = append(all, resolved{Dataset: name, Directory: directory})
	}

	return all, nil
}

func (p dataPaths) follow(edition, version, dataset string) (string, error) {
	current := version
	for hop := 0; hop < maxAliasHops; hop++ {
		directory, ok := p[edition][current][dataset]
		if !ok {
			return "", fmt.Errorf("dataset %s: %s/%s does not list it", dataset, edition, current)
		}
		next, err := splitDirectory(edition, directory)
		if err != nil {
			return "", fmt.Errorf("dataset %s: %w", dataset, err)
		}
		// Two things end a chain. A directory that maps the dataset back to
		// itself is upstream's usual way of saying "the data is here". A
		// directory dataPaths does not list at all is the same statement made
		// by omission — "proto" resolves to pc/latest, which is a real
		// directory and not a version anyone indexes.
		if next == current {
			return directory, nil
		}
		if _, listed := p[edition][next][dataset]; !listed {
			return directory, nil
		}
		current = next
	}

	return "", fmt.Errorf("dataset %s: %s/%s alias chain does not terminate", dataset, edition, version)
}

// splitDirectory checks that a dataPaths value names a directory in the
// expected edition and returns the version segment. A value naming another
// edition, or escaping the data tree, is refused: everything downstream builds
// a URL and a local path out of it.
func splitDirectory(edition, directory string) (string, error) {
	prefix, version, found := strings.Cut(directory, "/")
	if !found || prefix != edition || version == "" {
		return "", fmt.Errorf("directory %q is not under %s/", directory, edition)
	}
	if version != path.Clean(version) || strings.Contains(version, "/") || version == ".." {
		return "", fmt.Errorf("directory %q is not a plain version directory", directory)
	}

	return version, nil
}

// fileName is the upstream file for a dataset. Every dataset is JSON except
// proto, which is the ProtoDef schema in YAML.
func fileName(dataset string) string {
	if dataset == "proto" {
		return dataset + ".yml"
	}

	return dataset + ".json"
}

func mediaType(file string) string {
	if strings.HasSuffix(file, ".yml") {
		return "application/yaml"
	}

	return "application/json"
}
