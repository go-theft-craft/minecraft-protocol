package generator

import (
	"fmt"
	"io/fs"
	"path"
	"text/template"
)

// templateSet resolves a template name against a version's own directory
// first, then the shared one.
//
// Two supported versions do not describe the same game. Java 26.1 blocks carry
// block states where 1.8 blocks carry metadata variants, and its entities
// publish metadata keys that 1.8 has no notion of. A version overrides only the
// datasets whose shape actually changed, and inherits the rest, so the shared
// templates stay the description of what is common rather than a union of
// everything any version needs.
type templateSet struct {
	shared    *template.Template
	versioned *template.Template
	// packageName is the version directory that was searched, for errors.
	packageName string
}

// newTemplateSet parses the shared templates and, when the version has a
// directory of its own, that version's overrides.
func newTemplateSet(source fs.FS, packageName string) (templateSet, error) {
	shared, err := template.ParseFS(source, "templates/*.tmpl")
	if err != nil {
		return templateSet{}, fmt.Errorf("parse shared templates: %w", err)
	}

	set := templateSet{shared: shared, packageName: packageName}

	pattern := path.Join("templates", packageName, "*.tmpl")
	matches, err := fs.Glob(source, pattern)
	if err != nil {
		return templateSet{}, fmt.Errorf("search %s templates: %w", packageName, err)
	}
	if len(matches) == 0 {
		return set, nil
	}

	versioned, err := template.ParseFS(source, pattern)
	if err != nil {
		return templateSet{}, fmt.Errorf("parse %s templates: %w", packageName, err)
	}
	set.versioned = versioned

	return set, nil
}

// render executes the named template, preferring the version's own.
func (t templateSet) render(name string, value any) ([]byte, error) {
	if t.versioned != nil {
		if defined := t.versioned.Lookup(name); defined != nil {
			return renderTemplate(defined, name, value)
		}
	}

	defined := t.shared.Lookup(name)
	if defined == nil {
		return nil, fmt.Errorf("no template %s for package %s", name, t.packageName)
	}

	return renderTemplate(defined, name, value)
}

// overrides reports whether the version supplies its own copy of a template.
// Nothing in generation needs this; tests do, to prove the lookup order.
func (t templateSet) overrides(name string) bool {
	return t.versioned != nil && t.versioned.Lookup(name) != nil
}
