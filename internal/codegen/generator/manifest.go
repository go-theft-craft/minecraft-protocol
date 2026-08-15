package generator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

// verifiedSource is a source tree whose manifest loaded, whose every dataset
// matched its recorded checksum, and whose bytes are held in memory keyed by
// dataset name.
type verifiedSource struct {
	Manifest *manifest.Manifest
	Files    map[string][]byte
}

// dataset returns a dataset's bytes, or an error naming what is missing. The
// manifest states which datasets a tree has, so a generator asking for one it
// does not name is a generator bug rather than bad input.
func (s *verifiedSource) dataset(name string) ([]byte, error) {
	body, ok := s.Files[name]
	if !ok {
		return nil, fmt.Errorf("source tree has no dataset %q", name)
	}

	return body, nil
}

func loadVerifiedSource(sourceDir string) (*verifiedSource, error) {
	loaded, err := manifest.Load(sourceDir)
	if err != nil {
		return nil, err
	}
	if err := loaded.Verify(sourceDir); err != nil {
		return nil, err
	}

	files := make(map[string][]byte, len(loaded.Datasets))
	for _, dataset := range loaded.Datasets {
		body, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(dataset.File)))
		if err != nil {
			return nil, fmt.Errorf("read dataset %s: %w", dataset.Name, err)
		}
		files[dataset.Name] = body
	}

	return &verifiedSource{Manifest: loaded, Files: files}, nil
}

func validateManifest(sourceDir string) (*manifest.Manifest, error) {
	source, err := loadVerifiedSource(sourceDir)
	if err != nil {
		return nil, err
	}

	return source.Manifest, nil
}
