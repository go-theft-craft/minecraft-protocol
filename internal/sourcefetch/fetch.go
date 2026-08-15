// Package sourcefetch downloads a pinned PrismarineJS data tree and writes a
// manifest describing exactly what it got.
//
// The fetch is pinned by full commit, every body is hashed before it is used,
// and the tree appears at its destination only once all of it has been written.
// A run that fails leaves the previous tree untouched rather than a half-built
// one that would generate quietly wrong code.
package sourcefetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

const (
	// DefaultBaseURL serves raw files from the pinned repository.
	DefaultBaseURL = "https://raw.githubusercontent.com/PrismarineJS/minecraft-data"

	// SourceRepository is what the manifest records as the tree's origin.
	SourceRepository = "https://github.com/PrismarineJS/minecraft-data"

	// License is upstream's license. It is recorded, not assumed: a change
	// upstream must show up as a manifest diff.
	License = "MIT"

	// dataRoot is the directory inside the repository holding every dataset.
	dataRoot = "data"

	// localDataDir is where a fetched tree keeps its datasets, so the manifest
	// sits beside them rather than among them.
	localDataDir = "data"

	// maxDatasetBytes bounds one download. The largest dataset upstream ships
	// is a few megabytes; this is generous enough to be a backstop rather than
	// a limit anyone meets.
	maxDatasetBytes = 64 << 20

	// requestTimeout bounds one request, so a stalled connection fails the
	// fetch instead of holding it open.
	requestTimeout = 60 * time.Second
)

// Options selects what to fetch and where to put it.
type Options struct {
	Edition   string
	Version   string
	Protocol  int32
	Revision  string
	OutputDir string

	// BaseURL overrides the raw-file host. Tests point it at an httptest
	// server; leaving it empty uses DefaultBaseURL.
	BaseURL string

	// Client overrides the HTTP client, for tests and for callers that need
	// their own transport.
	Client *http.Client
}

func (o Options) baseURL() string {
	if o.BaseURL != "" {
		return strings.TrimSuffix(o.BaseURL, "/")
	}

	return DefaultBaseURL
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}

	return &http.Client{Timeout: requestTimeout}
}

func (o Options) validate() error {
	if o.Edition != "java" {
		return fmt.Errorf("unsupported edition %q", o.Edition)
	}
	if o.Version == "" {
		return fmt.Errorf("version is required")
	}
	if strings.ContainsAny(o.Version, `/\`) {
		return fmt.Errorf("version %q must not name a path", o.Version)
	}
	if o.Protocol <= 0 {
		return fmt.Errorf("protocol %d must be positive", o.Protocol)
	}
	if len(o.Revision) != 40 {
		return fmt.Errorf("revision %q must be a full commit hash", o.Revision)
	}
	if o.OutputDir == "" {
		return fmt.Errorf("output directory is required")
	}

	return nil
}

// versionDataset is the shape of upstream's version.json. It is the fetch's
// own check that the tree really is the protocol the caller asked for.
type versionDataset struct {
	Version          int32  `json:"version"`
	MinecraftVersion string `json:"minecraftVersion"`
	MajorVersion     string `json:"majorVersion"`
	ReleaseType      string `json:"releaseType"`
}

// Fetch downloads every dataset upstream lists for the requested version and
// replaces OutputDir with the result. It returns the manifest it wrote.
func Fetch(ctx context.Context, opts Options) (*manifest.Manifest, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}

	raw, err := get(ctx, opts, path.Join(dataRoot, "dataPaths.json"))
	if err != nil {
		return nil, err
	}
	paths, err := parseDataPaths(raw)
	if err != nil {
		return nil, err
	}
	edition, err := upstreamEdition(opts.Edition)
	if err != nil {
		return nil, err
	}
	entries, err := paths.resolve(edition, opts.Version)
	if err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp(filepath.Dir(opts.OutputDir), "."+filepath.Base(opts.OutputDir)+".tmp-")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if err := os.MkdirAll(filepath.Join(staging, localDataDir), 0o755); err != nil {
		return nil, fmt.Errorf("create staging data directory: %w", err)
	}

	datasets, bodies, err := download(ctx, opts, entries, staging)
	if err != nil {
		return nil, err
	}

	written, err := buildManifest(opts, datasets, bodies)
	if err != nil {
		return nil, err
	}
	if err := writeManifest(staging, written); err != nil {
		return nil, err
	}

	// Verify the staged tree through the same path generation will use, so a
	// tree that cannot be verified never reaches its destination.
	if err := written.Verify(staging); err != nil {
		return nil, fmt.Errorf("staged tree does not verify: %w", err)
	}
	if err := swap(staging, opts.OutputDir); err != nil {
		return nil, err
	}

	return written, nil
}

func download(ctx context.Context, opts Options, entries []resolved, staging string) ([]manifest.Dataset, map[string][]byte, error) {
	datasets := make([]manifest.Dataset, 0, len(entries))
	bodies := make(map[string][]byte, len(entries))

	for _, entry := range entries {
		file := fileName(entry.Dataset)
		remote := path.Join(dataRoot, entry.Directory, file)

		body, err := get(ctx, opts, remote)
		if err != nil {
			return nil, nil, fmt.Errorf("dataset %s: %w", entry.Dataset, err)
		}

		local := path.Join(localDataDir, file)
		target := filepath.Join(staging, filepath.FromSlash(local))
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return nil, nil, fmt.Errorf("dataset %s: write: %w", entry.Dataset, err)
		}

		// Read the file back rather than trusting the bytes in hand. A short
		// write or a filesystem that reordered the data would otherwise be
		// recorded under the checksum of what was meant to be written.
		stored, err := os.ReadFile(target)
		if err != nil {
			return nil, nil, fmt.Errorf("dataset %s: read back: %w", entry.Dataset, err)
		}
		digest := sha256.Sum256(stored)
		if sha256.Sum256(body) != digest {
			return nil, nil, fmt.Errorf("dataset %s: stored bytes differ from what was fetched", entry.Dataset)
		}

		datasets = append(datasets, manifest.Dataset{
			Name:       entry.Dataset,
			File:       local,
			SourcePath: path.Join(dataRoot, entry.Directory, file),
			MediaType:  mediaType(file),
			SHA256:     hex.EncodeToString(digest[:]),
		})
		bodies[entry.Dataset] = stored
	}

	return datasets, bodies, nil
}

func buildManifest(opts Options, datasets []manifest.Dataset, bodies map[string][]byte) (*manifest.Manifest, error) {
	rawVersion, ok := bodies["version"]
	if !ok {
		return nil, fmt.Errorf("%s/%s has no version dataset, so its protocol cannot be checked", opts.Edition, opts.Version)
	}

	var version versionDataset
	if err := json.Unmarshal(rawVersion, &version); err != nil {
		return nil, fmt.Errorf("parse version dataset: %w", err)
	}
	if version.Version != opts.Protocol {
		return nil, fmt.Errorf("%s/%s is protocol %d, not %d", opts.Edition, opts.Version, version.Version, opts.Protocol)
	}
	if version.MinecraftVersion == "" {
		return nil, fmt.Errorf("version dataset names no Minecraft version")
	}

	written := &manifest.Manifest{
		ManifestVersion:        manifest.Version,
		Edition:                opts.Edition,
		TargetMinecraftVersion: opts.Version,
		SourceMinecraftVersion: version.MinecraftVersion,
		SourceVersionDirectory: opts.Version,
		Protocol:               version.Version,
		SourceRepository:       SourceRepository,
		SourceRevision:         opts.Revision,
		License:                License,
		Datasets:               datasets,
	}

	return written, nil
}

func writeManifest(dir string, written *manifest.Manifest) error {
	raw, err := json.MarshalIndent(written, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	return os.WriteFile(filepath.Join(dir, manifest.FileName), append(raw, '\n'), 0o644)
}

// swap moves the staged tree onto the destination. The previous tree is moved
// aside first and removed only once the new one is in place, so an interrupted
// swap leaves one of the two complete trees rather than neither.
func swap(staging, output string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}

	previous := ""
	if _, err := os.Stat(output); err == nil {
		previous = output + ".previous"
		if err := os.RemoveAll(previous); err != nil {
			return fmt.Errorf("clear previous tree: %w", err)
		}
		if err := os.Rename(output, previous); err != nil {
			return fmt.Errorf("move previous tree aside: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output directory: %w", err)
	}

	if err := os.Rename(staging, output); err != nil {
		if previous != "" {
			_ = os.Rename(previous, output)
		}

		return fmt.Errorf("move fetched tree into place: %w", err)
	}

	if previous != "" {
		if err := os.RemoveAll(previous); err != nil {
			return fmt.Errorf("remove previous tree: %w", err)
		}
	}

	return nil
}

func get(ctx context.Context, opts Options, remote string) ([]byte, error) {
	target, err := url.JoinPath(opts.baseURL(), opts.Revision, remote)
	if err != nil {
		return nil, fmt.Errorf("build URL for %s: %w", remote, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", remote, err)
	}

	response, err := opts.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", remote, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", remote, response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxDatasetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", remote, err)
	}
	if len(body) > maxDatasetBytes {
		return nil, fmt.Errorf("read %s: larger than %d bytes", remote, maxDatasetBytes)
	}

	return body, nil
}
