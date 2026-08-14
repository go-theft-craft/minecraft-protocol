package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	supportedEdition       = "java"
	supportedTargetVersion = "1.8.9"
	supportedSourceVersion = "1.8.8"
	supportedProtocol      = 47
	supportedRepository    = "https://github.com/PrismarineJS/minecraft-data"
	supportedRevision      = "3f0dd2ac525607b21be7cd6ddd003fa9057a72d2"
	supportedSourcePath    = "data/pc/1.8"
	supportedLicense       = "MIT"
)

var requiredSourceFiles = []string{
	"attributes.json",
	"biomes.json",
	"blockCollisionShapes.json",
	"blocks.json",
	"effects.json",
	"enchantments.json",
	"entities.json",
	"foods.json",
	"instruments.json",
	"items.json",
	"language.json",
	"materials.json",
	"particles.json",
	"protocol.json",
	"recipes.json",
	"version.json",
	"windows.json",
}

type sourceManifest struct {
	Edition                string            `json:"edition"`
	TargetMinecraftVersion string            `json:"targetMinecraftVersion"`
	SourceMinecraftVersion string            `json:"sourceMinecraftVersion"`
	Protocol               int               `json:"protocol"`
	SourceRepository       string            `json:"sourceRepository"`
	SourceRevision         string            `json:"sourceRevision"`
	SourcePath             string            `json:"sourcePath"`
	License                string            `json:"license"`
	Files                  map[string]string `json:"files"`
}

type verifiedSource struct {
	Manifest *sourceManifest
	Files    map[string][]byte
}

func loadVerifiedSource(sourceDir string) (*verifiedSource, error) {
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var manifest sourceManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Edition != supportedEdition {
		return nil, fmt.Errorf("unsupported edition %q", manifest.Edition)
	}
	if manifest.TargetMinecraftVersion != supportedTargetVersion {
		return nil, fmt.Errorf("unsupported target Minecraft version %q", manifest.TargetMinecraftVersion)
	}
	if manifest.SourceMinecraftVersion != supportedSourceVersion {
		return nil, fmt.Errorf("unsupported source Minecraft version %q", manifest.SourceMinecraftVersion)
	}
	if manifest.Protocol != supportedProtocol {
		return nil, fmt.Errorf("unsupported protocol %d", manifest.Protocol)
	}
	if manifest.SourceRepository != supportedRepository {
		return nil, fmt.Errorf("unsupported source repository %q", manifest.SourceRepository)
	}
	if manifest.SourceRevision != supportedRevision {
		return nil, fmt.Errorf("unsupported source revision %q", manifest.SourceRevision)
	}
	if manifest.SourcePath != supportedSourcePath {
		return nil, fmt.Errorf("unsupported source path %q", manifest.SourcePath)
	}
	if manifest.License != supportedLicense {
		return nil, fmt.Errorf("unsupported source license %q", manifest.License)
	}

	required := make(map[string]struct{}, len(requiredSourceFiles))
	for _, name := range requiredSourceFiles {
		required[name] = struct{}{}
		if _, ok := manifest.Files[name]; !ok {
			return nil, fmt.Errorf("manifest is missing required file %s", name)
		}
	}
	for name := range manifest.Files {
		if _, ok := required[name]; !ok {
			return nil, fmt.Errorf("manifest contains unexpected file %s", name)
		}
	}
	if len(manifest.Files) != len(requiredSourceFiles) {
		return nil, fmt.Errorf("manifest contains %d files, want %d", len(manifest.Files), len(requiredSourceFiles))
	}

	files := make(map[string][]byte, len(requiredSourceFiles))
	for _, name := range requiredSourceFiles {
		checksum := manifest.Files[name]
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".json") || name == "manifest.json" {
			return nil, fmt.Errorf("invalid manifest file name %q", name)
		}
		decoded, err := hex.DecodeString(checksum)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("malformed checksum for %s", name)
		}

		fileRaw, err := os.ReadFile(filepath.Join(sourceDir, name))
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("missing manifest file %s", name)
		}
		if err != nil {
			return nil, fmt.Errorf("read manifest file %s: %w", name, err)
		}
		sum := sha256.Sum256(fileRaw)
		if !strings.EqualFold(checksum, hex.EncodeToString(sum[:])) {
			return nil, fmt.Errorf("checksum mismatch for %s", name)
		}
		files[name] = fileRaw
	}

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("read source directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "manifest.json" {
			continue
		}
		if _, ok := manifest.Files[entry.Name()]; !ok {
			return nil, fmt.Errorf("unexpected JSON file %s", entry.Name())
		}
	}

	return &verifiedSource{Manifest: &manifest, Files: files}, nil
}

func validateManifest(sourceDir string) (*sourceManifest, error) {
	source, err := loadVerifiedSource(sourceDir)
	if err != nil {
		return nil, err
	}
	return source.Manifest, nil
}
