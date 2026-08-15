package generator

import (
	"strings"
	"testing"
	"testing/fstest"
)

// fakeTemplates is a shared directory plus one version that overrides a single
// template, which is the arrangement version scoping exists for.
func fakeTemplates() fstest.MapFS {
	// Rendering gofmts what it produces, so each fixture is valid Go.
	return fstest.MapFS{
		"templates/blocks.go.tmpl":       {Data: []byte("package {{ .Package }}\n\n// shared blocks\n")},
		"templates/items.go.tmpl":        {Data: []byte("package {{ .Package }}\n\n// shared items\n")},
		"templates/v26_1/blocks.go.tmpl": {Data: []byte("package {{ .Package }}\n\n// v26_1 blocks\n")},
	}
}

func TestTemplateSetPrefersTheVersionsOwnTemplate(t *testing.T) {
	set, err := newTemplateSet(fakeTemplates(), "v26_1")
	if err != nil {
		t.Fatalf("newTemplateSet: %v", err)
	}

	if !set.overrides("blocks.go.tmpl") {
		t.Error("v26_1 does not override blocks")
	}
	if set.overrides("items.go.tmpl") {
		t.Error("v26_1 overrides items, which it does not define")
	}

	rendered, err := set.render("blocks.go.tmpl", templateData{Package: "v26_1"})
	if err != nil {
		t.Fatalf("render blocks: %v", err)
	}
	if !strings.Contains(string(rendered), "// v26_1 blocks") {
		t.Errorf("blocks rendered %q, want the version's own template", rendered)
	}
}

// TestTemplateSetFallsBackToShared is the other half: a version overrides only
// what changed shape and inherits the rest.
func TestTemplateSetFallsBackToShared(t *testing.T) {
	set, err := newTemplateSet(fakeTemplates(), "v26_1")
	if err != nil {
		t.Fatalf("newTemplateSet: %v", err)
	}

	rendered, err := set.render("items.go.tmpl", templateData{Package: "v26_1"})
	if err != nil {
		t.Fatalf("render items: %v", err)
	}
	if !strings.Contains(string(rendered), "// shared items") {
		t.Errorf("items rendered %q, want the shared template", rendered)
	}
}

// TestTemplateSetWithoutAVersionDirectoryUsesShared covers the state every
// version starts in, and the one Java 1.8 stays in.
func TestTemplateSetWithoutAVersionDirectoryUsesShared(t *testing.T) {
	set, err := newTemplateSet(fakeTemplates(), "v1_8")
	if err != nil {
		t.Fatalf("newTemplateSet: %v", err)
	}

	if set.overrides("blocks.go.tmpl") {
		t.Fatal("v1_8 has no template directory but reported an override")
	}

	rendered, err := set.render("blocks.go.tmpl", templateData{Package: "v1_8"})
	if err != nil {
		t.Fatalf("render blocks: %v", err)
	}
	if !strings.Contains(string(rendered), "// shared blocks") {
		t.Errorf("blocks rendered %q, want the shared template", rendered)
	}
}

func TestTemplateSetReportsAMissingTemplate(t *testing.T) {
	set, err := newTemplateSet(fakeTemplates(), "v1_8")
	if err != nil {
		t.Fatalf("newTemplateSet: %v", err)
	}

	_, err = set.render("absent.go.tmpl", templateData{Package: "v1_8"})
	if err == nil {
		t.Fatal("render accepted a template that does not exist")
	}
	if !strings.Contains(err.Error(), "absent.go.tmpl") || !strings.Contains(err.Error(), "v1_8") {
		t.Errorf("error = %q, want it to name the template and the package", err)
	}
}

// TestRealTemplatesResolveForJava18 checks the embedded tree rather than a
// fixture, so a template deleted or renamed fails here.
func TestRealTemplatesResolveForJava18(t *testing.T) {
	set, err := newTemplateSet(templateFS, "v1_8")
	if err != nil {
		t.Fatalf("newTemplateSet: %v", err)
	}

	for _, name := range []string{"blocks.go.tmpl", "items.go.tmpl", "protocol.go.tmpl", "gamedata.go.tmpl"} {
		if set.overrides(name) {
			t.Errorf("v1_8 overrides %s, which it should inherit", name)
		}
		if set.shared.Lookup(name) == nil {
			t.Errorf("shared templates do not define %s", name)
		}
	}
}
