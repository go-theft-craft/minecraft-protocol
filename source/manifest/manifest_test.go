package manifest_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

// write lays out a source tree: manifest.json holding value, plus every file
// in files. It returns the directory.
func write(t *testing.T, value any, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return dir
}

func sum(body string) string {
	digest := sha256.Sum256([]byte(body))

	return hex.EncodeToString(digest[:])
}

// valid returns a manifest with two datasets, one of them an alias of an
// older upstream version, and the file bodies that match its hashes.
func valid() (map[string]any, map[string]string) {
	blocks := `[{"id":1}]`
	windows := `[{"id":"minecraft:generic_9x3"}]`

	value := map[string]any{
		"manifestVersion":        2,
		"edition":                "java",
		"targetMinecraftVersion": "26.1",
		"sourceMinecraftVersion": "26.1",
		"sourceVersionDirectory": "26.1",
		"protocol":               775,
		"sourceRepository":       "https://github.com/PrismarineJS/minecraft-data",
		"sourceRevision":         "8a80816cbfb3fe2b609f2cde4e57796c8033af61",
		"license":                "MIT",
		"datasets": []map[string]any{
			{
				"name":       "blocks",
				"file":       "data/blocks.json",
				"sourcePath": "data/pc/26.1/blocks.json",
				"mediaType":  "application/json",
				"sha256":     sum(blocks),
			},
			{
				"name":       "windows",
				"file":       "data/windows.json",
				"sourcePath": "data/pc/1.16.1/windows.json",
				"mediaType":  "application/json",
				"sha256":     sum(windows),
			},
		},
	}

	return value, map[string]string{"data/blocks.json": blocks, "data/windows.json": windows}
}

func TestLoadAcceptsAMultiAliasManifest(t *testing.T) {
	value, files := valid()
	dir := write(t, value, files)

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Protocol != 775 {
		t.Errorf("protocol = %d, want 775", loaded.Protocol)
	}
	if len(loaded.Datasets) != 2 {
		t.Fatalf("datasets = %d, want 2", len(loaded.Datasets))
	}

	blocks, ok := loaded.Dataset("blocks")
	if !ok {
		t.Fatal("blocks is missing")
	}
	if blocks.Aliased("26.1") {
		t.Error("blocks came from the target version and is not an alias")
	}

	windows, ok := loaded.Dataset("windows")
	if !ok {
		t.Fatal("windows is missing")
	}
	if !windows.Aliased("26.1") {
		t.Error("windows came from 1.16.1 and is an alias")
	}

	if _, ok := loaded.Dataset("absent"); ok {
		t.Error("Dataset reported a dataset the manifest does not name")
	}
	if err := loaded.Verify(dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(value map[string]any, files map[string]string)
		wantErr string
	}{
		{
			name: "an unsupported manifest version",
			mutate: func(value map[string]any, _ map[string]string) {
				value["manifestVersion"] = 1
			},
			wantErr: "manifest version",
		},
		{
			name: "an unsupported edition",
			mutate: func(value map[string]any, _ map[string]string) {
				value["edition"] = "bedrock"
			},
			wantErr: "edition",
		},
		{
			name: "a missing target version",
			mutate: func(value map[string]any, _ map[string]string) {
				value["targetMinecraftVersion"] = ""
			},
			wantErr: "targetMinecraftVersion",
		},
		{
			name: "a non-positive protocol",
			mutate: func(value map[string]any, _ map[string]string) {
				value["protocol"] = 0
			},
			wantErr: "protocol",
		},
		{
			name: "a source revision that is not a full commit",
			mutate: func(value map[string]any, _ map[string]string) {
				value["sourceRevision"] = "main"
			},
			wantErr: "sourceRevision",
		},
		{
			name: "a duplicate dataset name",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[1]["name"] = "blocks"
			},
			wantErr: "duplicate",
		},
		{
			name: "a file escaping the source directory",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[0]["file"] = "../blocks.json"
			},
			wantErr: "file",
		},
		{
			name: "an absolute file",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[0]["file"] = "/etc/passwd"
			},
			wantErr: "file",
		},
		{
			name: "a source path outside data/pc",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[0]["sourcePath"] = "data/bedrock/26.1/blocks.json"
			},
			wantErr: "sourcePath",
		},
		{
			name: "a source path escaping data/pc",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[0]["sourcePath"] = "data/pc/../../etc/blocks.json"
			},
			wantErr: "sourcePath",
		},
		{
			name: "a checksum that is not 64 hex characters",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[0]["sha256"] = "abc123"
			},
			wantErr: "sha256",
		},
		{
			name: "an uppercase checksum",
			mutate: func(value map[string]any, _ map[string]string) {
				datasets := value["datasets"].([]map[string]any)
				datasets[0]["sha256"] = strings.ToUpper(datasets[0]["sha256"].(string))
			},
			wantErr: "sha256",
		},
		{
			name: "no datasets at all",
			mutate: func(value map[string]any, _ map[string]string) {
				value["datasets"] = []map[string]any{}
			},
			wantErr: "datasets",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, files := valid()
			tc.mutate(value, files)
			dir := write(t, value, files)

			_, err := manifest.Load(dir)
			if err == nil {
				t.Fatal("Load accepted the manifest")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to name %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRejectsAnUnknownField(t *testing.T) {
	value, files := valid()
	value["sourcePath"] = "data/pc/26.1"
	dir := write(t, value, files)

	if _, err := manifest.Load(dir); err == nil {
		t.Fatal("Load accepted a manifest carrying a field it does not define")
	}
}

func TestVerifyCatchesAChangedByte(t *testing.T) {
	value, files := valid()
	dir := write(t, value, files)

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "data/blocks.json"), []byte(`[{"id":2}]`), 0o600); err != nil {
		t.Fatalf("rewrite blocks: %v", err)
	}

	err = loaded.Verify(dir)
	if err == nil {
		t.Fatal("Verify accepted a changed file")
	}
	if !strings.Contains(err.Error(), "blocks") {
		t.Errorf("error = %q, want it to name the dataset", err)
	}
}

func TestVerifyCatchesAMissingFile(t *testing.T) {
	value, files := valid()
	dir := write(t, value, files)

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := os.Remove(filepath.Join(dir, "data/windows.json")); err != nil {
		t.Fatalf("remove windows: %v", err)
	}

	if err := loaded.Verify(dir); err == nil {
		t.Fatal("Verify accepted a manifest naming a file that is not there")
	}
}

func TestVerifyCatchesAnUnrecordedFile(t *testing.T) {
	value, files := valid()
	dir := write(t, value, files)

	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "data/extra.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write extra: %v", err)
	}

	err = loaded.Verify(dir)
	if err == nil {
		t.Fatal("Verify accepted a file the manifest does not record")
	}
	if !strings.Contains(err.Error(), "extra.json") {
		t.Errorf("error = %q, want it to name the file", err)
	}
}
