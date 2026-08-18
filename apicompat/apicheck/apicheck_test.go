package apicheck_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/apicompat/apicheck"
)

func TestThePublicSurfaceHasNotChangedIncompatibly(t *testing.T) {
	t.Parallel()

	// The baseline is committed, so an incompatible change fails here and is
	// either reverted or accepted deliberately by rewriting the baseline in
	// the same commit, where a reviewer sees both halves at once.
	baseline, err := apicheck.ReadBaseline("../../api")
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	current, _, err := apicheck.Load("../..")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result := apicheck.Compare(baseline, current)
	if len(result.Incompatible) != 0 {
		t.Errorf("incompatible API changes since the baseline; revert them or "+
			"accept them with task api:accept in this same commit:\n%s",
			strings.Join(result.Incompatible, "\n"))
	}
	for _, change := range result.Compatible {
		t.Log("compatible: " + change)
	}
}

// typecheck builds a throwaway package from source, for the classification
// tests: they must not depend on the real surface changing.
func typecheck(t *testing.T, src string) *types.Package {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check(
		"example.test/x", fset, []*ast.File{file}, nil,
	)
	if err != nil {
		t.Fatalf("typecheck: %v", err)
	}

	return pkg
}

func TestAnAddedMethodIsNewsAndNotAFailure(t *testing.T) {
	t.Parallel()

	old := typecheck(t, `package x
type T struct{}
`)
	now := typecheck(t, `package x
type T struct{}
func (T) Grew() {}
`)

	result := apicheck.Compare(
		map[string]*types.Package{"example.test/x": old},
		map[string]*types.Package{"example.test/x": now},
	)
	if len(result.Incompatible) != 0 {
		t.Fatalf("an added method reported as incompatible: %v", result.Incompatible)
	}
	if len(result.Compatible) == 0 {
		t.Fatal("an added method reported as nothing at all")
	}
}

func TestARemovedSymbolIsIncompatible(t *testing.T) {
	t.Parallel()

	old := typecheck(t, `package x
func Gone() {}
`)
	now := typecheck(t, `package x
`)

	result := apicheck.Compare(
		map[string]*types.Package{"example.test/x": old},
		map[string]*types.Package{"example.test/x": now},
	)
	if len(result.Incompatible) == 0 {
		t.Fatal("a removed function reported as compatible")
	}
}

func TestARemovedPackageIsIncompatible(t *testing.T) {
	t.Parallel()

	old := typecheck(t, `package x`)
	result := apicheck.Compare(
		map[string]*types.Package{"example.test/x": old},
		map[string]*types.Package{},
	)
	if len(result.Incompatible) != 1 {
		t.Fatalf("a removed package reported %v", result.Incompatible)
	}
}
