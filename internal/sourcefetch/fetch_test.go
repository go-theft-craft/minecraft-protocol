package sourcefetch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/internal/sourcefetch"
	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

const revision = "8a80816cbfb3fe2b609f2cde4e57796c8033af61"

// upstream serves a fixture repository at one revision. Every test drives the
// fetcher against one of these; nothing reaches the network.
type upstream struct {
	files  map[string]string
	status map[string]int
	hits   map[string]int
	server *httptest.Server
}

func newUpstream(t *testing.T, files map[string]string) *upstream {
	t.Helper()

	up := &upstream{files: files, status: map[string]int{}, hits: map[string]int{}}
	up.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/"+revision+"/")
		up.hits[name]++

		if code, ok := up.status[name]; ok {
			w.WriteHeader(code)

			return
		}
		body, ok := up.files[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)

			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(up.server.Close)

	return up
}

func (u *upstream) options(output string) sourcefetch.Options {
	return sourcefetch.Options{
		Edition:   "java",
		Version:   "26.1",
		Protocol:  775,
		Revision:  revision,
		OutputDir: output,
		BaseURL:   u.server.URL,
		Client:    u.server.Client(),
	}
}

// fixture is a small repository: three datasets for 26.1, one of which upstream
// keeps under an older directory, and one reached through a two-hop chain.
func fixture() map[string]string {
	paths := map[string]any{
		"pc": map[string]any{
			"26.1": map[string]string{
				"version":  "pc/26.1",
				"blocks":   "pc/26.1",
				"windows":  "pc/1.16.1",
				"mapIcons": "pc/1.20.2",
				// latest is a real directory upstream does not index, which
				// is how a chain ends without a self-reference.
				"proto": "pc/latest",
			},
			"1.16.1": map[string]string{"windows": "pc/1.16.1"},
			// mapIcons hops through 1.20.2 to 1.20.1 before terminating.
			"1.20.2": map[string]string{"mapIcons": "pc/1.20.1"},
			"1.20.1": map[string]string{"mapIcons": "pc/1.20.1"},
		},
	}
	raw, err := json.Marshal(paths)
	if err != nil {
		panic(err)
	}

	return map[string]string{
		"data/dataPaths.json":          string(raw),
		"data/pc/26.1/version.json":    `{"version":775,"minecraftVersion":"26.1","majorVersion":"26.1","releaseType":"release"}`,
		"data/pc/26.1/blocks.json":     `[{"id":1,"name":"stone"}]`,
		"data/pc/1.16.1/windows.json":  `[{"id":"minecraft:generic_9x3"}]`,
		"data/pc/1.20.1/mapIcons.json": `[{"id":0,"name":"white_arrow"}]`,
		"data/pc/latest/proto.yml":     "!version: 775\n",
	}
}

func TestFetchWritesAVerifiableTree(t *testing.T) {
	up := newUpstream(t, fixture())
	output := filepath.Join(t.TempDir(), "26.1")

	written, err := sourcefetch.Fetch(context.Background(), up.options(output))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(written.Datasets) != 5 {
		t.Fatalf("datasets = %d, want 5", len(written.Datasets))
	}
	if written.Protocol != 775 {
		t.Errorf("protocol = %d, want 775", written.Protocol)
	}
	if written.SourceRevision != revision {
		t.Errorf("revision = %q, want the pinned commit", written.SourceRevision)
	}

	loaded, err := manifest.Load(output)
	if err != nil {
		t.Fatalf("the written manifest does not load: %v", err)
	}
	if err := loaded.Verify(output); err != nil {
		t.Fatalf("the written tree does not verify: %v", err)
	}

	windows, ok := loaded.Dataset("windows")
	if !ok {
		t.Fatal("windows is missing")
	}
	if !windows.Aliased("26.1") || windows.SourceVersion() != "1.16.1" {
		t.Errorf("windows source = %q, want the 1.16.1 alias", windows.SourcePath)
	}

	// A two-hop chain resolves to where the data actually is, not to the
	// first directory the chain names.
	icons, ok := loaded.Dataset("mapIcons")
	if !ok {
		t.Fatal("mapIcons is missing")
	}
	if icons.SourceVersion() != "1.20.1" {
		t.Errorf("mapIcons source = %q, want the end of the alias chain", icons.SourcePath)
	}

	// proto is YAML and ends its chain at a directory dataPaths never lists.
	proto, ok := loaded.Dataset("proto")
	if !ok {
		t.Fatal("proto is missing")
	}
	if proto.File != "data/proto.yml" || proto.MediaType != "application/yaml" {
		t.Errorf("proto = %+v, want the YAML schema", proto)
	}
	if proto.SourceVersion() != "latest" {
		t.Errorf("proto source = %q, want latest", proto.SourcePath)
	}

	blocks, ok := loaded.Dataset("blocks")
	if !ok {
		t.Fatal("blocks is missing")
	}
	if blocks.Aliased("26.1") {
		t.Error("blocks came from the target version and is not an alias")
	}
	if blocks.File != "data/blocks.json" {
		t.Errorf("blocks file = %q, want it under data/", blocks.File)
	}
}

func TestFetchIsIdempotent(t *testing.T) {
	up := newUpstream(t, fixture())
	output := filepath.Join(t.TempDir(), "26.1")

	first, err := sourcefetch.Fetch(context.Background(), up.options(output))
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	before := snapshot(t, output)

	second, err := sourcefetch.Fetch(context.Background(), up.options(output))
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	after := snapshot(t, output)

	if len(before) != len(after) {
		t.Fatalf("tree holds %d files after a second fetch, want %d", len(after), len(before))
	}
	for name, body := range before {
		if after[name] != body {
			t.Errorf("%s changed on a second fetch", name)
		}
	}
	if first.Datasets[0].SHA256 != second.Datasets[0].SHA256 {
		t.Error("a second fetch recorded different bytes")
	}
	if _, err := os.Stat(output + ".previous"); !os.IsNotExist(err) {
		t.Error("the previous tree was left behind")
	}
}

func TestFetchLeavesTheOldTreeOnFailure(t *testing.T) {
	up := newUpstream(t, fixture())
	output := filepath.Join(t.TempDir(), "26.1")

	if _, err := sourcefetch.Fetch(context.Background(), up.options(output)); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	before := snapshot(t, output)

	// The third file fails. Nothing about the tree already in place may change.
	up.status["data/pc/1.16.1/windows.json"] = http.StatusInternalServerError

	if _, err := sourcefetch.Fetch(context.Background(), up.options(output)); err == nil {
		t.Fatal("Fetch reported success after a 500")
	}

	after := snapshot(t, output)
	if len(before) != len(after) {
		t.Fatalf("tree holds %d files after a failed fetch, want %d", len(after), len(before))
	}
	for name, body := range before {
		if after[name] != body {
			t.Errorf("%s changed during a failed fetch", name)
		}
	}

	parent, err := os.ReadDir(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range parent {
		if entry.Name() != "26.1" {
			t.Errorf("a failed fetch left %s behind", entry.Name())
		}
	}
}

func TestFetchRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(files map[string]string, opts *sourcefetch.Options)
		wantErr string
	}{
		{
			name: "a dataset upstream does not actually have",
			mutate: func(files map[string]string, _ *sourcefetch.Options) {
				delete(files, "data/pc/26.1/blocks.json")
			},
			wantErr: "blocks",
		},
		{
			name: "a version dataPaths does not list",
			mutate: func(_ map[string]string, opts *sourcefetch.Options) {
				opts.Version = "27.0"
			},
			wantErr: `version "27.0"`,
		},
		{
			name: "a protocol the data does not agree with",
			mutate: func(_ map[string]string, opts *sourcefetch.Options) {
				opts.Protocol = 774
			},
			wantErr: "protocol 775, not 774",
		},
		{
			name: "a directory outside the edition",
			mutate: func(files map[string]string, _ *sourcefetch.Options) {
				files["data/dataPaths.json"] = `{"pc":{"26.1":{"blocks":"bedrock/1.0"}}}`
			},
			wantErr: "not under pc/",
		},
		{
			name: "a directory escaping the data tree",
			mutate: func(files map[string]string, _ *sourcefetch.Options) {
				files["data/dataPaths.json"] = `{"pc":{"26.1":{"blocks":"pc/../../etc"}}}`
			},
			wantErr: "plain version directory",
		},
		{
			name: "an alias chain that does not terminate",
			mutate: func(files map[string]string, _ *sourcefetch.Options) {
				files["data/dataPaths.json"] = `{"pc":{"26.1":{"blocks":"pc/a"},"a":{"blocks":"pc/b"},"b":{"blocks":"pc/a"}}}`
			},
			wantErr: "does not terminate",
		},
		{
			name: "a tree with no version dataset",
			mutate: func(files map[string]string, _ *sourcefetch.Options) {
				files["data/dataPaths.json"] = `{"pc":{"26.1":{"blocks":"pc/26.1"}}}`
			},
			wantErr: "no version dataset",
		},
		{
			name: "a short revision",
			mutate: func(_ map[string]string, opts *sourcefetch.Options) {
				opts.Revision = "8a80816"
			},
			wantErr: "full commit hash",
		},
		{
			name: "an unsupported edition",
			mutate: func(_ map[string]string, opts *sourcefetch.Options) {
				opts.Edition = "bedrock"
			},
			wantErr: "unsupported edition",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := fixture()
			up := newUpstream(t, files)
			output := filepath.Join(t.TempDir(), "26.1")
			opts := up.options(output)
			tc.mutate(files, &opts)

			_, err := sourcefetch.Fetch(context.Background(), opts)
			if err == nil {
				t.Fatal("Fetch reported success")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to name %q", err, tc.wantErr)
			}
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Error("a failed fetch left an output tree behind")
			}
		})
	}
}

func TestFetchHonoursCancellation(t *testing.T) {
	up := newUpstream(t, fixture())
	output := filepath.Join(t.TempDir(), "26.1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := sourcefetch.Fetch(ctx, up.options(output)); err == nil {
		t.Fatal("Fetch ignored a cancelled context")
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := make(map[string]string)
	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		files[path.Clean(filepath.ToSlash(relative))] = string(body)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}
