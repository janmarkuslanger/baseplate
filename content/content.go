// Package content describes content types, fields and blocks, and validates
// entry data against them. Schemas are defined in code by the customer
// project and by plugins.
package content

import (
	"fmt"
	"strings"
)

// Field is one field of a content type, block or nested group.
type Field struct {
	Name     string
	Label    string
	Kind     string // name of a registered Kind
	Help     string
	Required bool
	// Tab groups fields into admin tabs; empty means the default tab.
	Tab string
	// Max is the maximum length for text kinds (0 = unlimited).
	Max int
	// Options for the "select" kind.
	Options []Option
	// Ref names the target content type of a relation.
	Ref string
	// Many makes a relation hold a list of targets instead of one.
	Many bool
	// Inverse names a read-only field on the target type that lists the
	// entries pointing at it (e.g. "articles" on a category).
	Inverse string
	// Blocks restricts the "blocks" kind to these block names (empty = all).
	Blocks []string
	// Fields are the sub-fields of the "group" and "list" kinds.
	Fields []Field
	// RenamedFrom is the field's previous name. On start, stored data under
	// the old name is moved to the new one. Each rename runs once and is
	// recorded; to reuse the old name for a new field, remove RenamedFrom
	// first (Schema.Check rejects both at once).
	RenamedFrom string
}

// Option is one choice of a select field.
type Option struct {
	Value string
	Label string
}

// Type is a content type such as "page" or "form".
type Type struct {
	Name        string
	Label       string
	LabelPlural string
	Fields      []Field
	// Singleton types have exactly one entry (e.g. site settings).
	Singleton bool
	// Hidden types are not shown in the admin navigation.
	Hidden bool
	// Routable types get a slug and are rendered to a page by the SSG build.
	Routable bool
	// TitleField is used in admin lists; defaults to "title".
	TitleField string
	// Template is the frontend template used to render routable entries;
	// defaults to the type name.
	Template string
	// Layout wraps the rendered template (see render.Layout); defaults to
	// the theme's "layout" if it defines one, "-" means none.
	Layout string
	// Path is the URL pattern of routable entries; defaults to
	// "/<name>/{slug}/". Placeholders: {slug}, and {parents} for the slugs
	// of the "parent" chain (a relation of the type to itself). Empty
	// segments are dropped and the slug "index" is empty, so the core page
	// type's "/{parents}/{slug}/" makes "index" the home page.
	Path string
}

// Block is a content element that can be placed in a "blocks" field.
type Block struct {
	Name   string
	Label  string
	Fields []Field
	// Template is the frontend template; defaults to "block/<name>".
	Template string
	// Markdown renders the block for the .md page variants and llms.txt
	// consumers. Nil means the block is left out of the Markdown output.
	Markdown func(data map[string]any) string
	// Restricted blocks are offered only by blocks fields that name them in
	// Field.Blocks (e.g. the items of a plugin's own type), never by fields
	// that allow all blocks, such as the page body.
	Restricted bool
}

// TemplateName returns Template or its default "block/<name>".
func (b Block) TemplateName() string {
	if b.Template != "" {
		return b.Template
	}
	return "block/" + b.Name
}

// Title returns the field shown as the entry title in admin lists.
func (t Type) Title() string {
	if t.TitleField != "" {
		return t.TitleField
	}
	return "title"
}

// PathPattern returns Path or its default.
func (t Type) PathPattern() string {
	if t.Path != "" {
		return t.Path
	}
	return "/" + t.Name + "/{slug}/"
}

// URL builds an entry's URL from its slug and its parents' slugs
// (outermost first).
func (t Type) URL(slug string, parents []string) string {
	if slug == "index" {
		slug = ""
	}
	p := strings.NewReplacer("{slug}", slug, "{parents}", strings.Join(parents, "/")).Replace(t.PathPattern())
	var parts []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "/"
	}
	return "/" + strings.Join(parts, "/") + "/"
}

// checkPath validates the URL pattern of a routable type.
func (t Type) checkPath() error {
	p := t.PathPattern()
	if !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") {
		return fmt.Errorf("content: path of %s must start and end with \"/\": %q", t.Name, p)
	}
	if !strings.Contains(p, "{slug}") {
		return fmt.Errorf("content: path of %s needs {slug}: %q", t.Name, p)
	}
	rest := strings.NewReplacer("{slug}", "", "{parents}", "").Replace(p)
	if strings.ContainsAny(rest, "{}") {
		return fmt.Errorf("content: path of %s has an unknown placeholder: %q", t.Name, p)
	}
	if strings.Contains(p, "{parents}") {
		if f := t.Field("parent"); f == nil || f.Ref != t.Name {
			return fmt.Errorf("content: path of %s uses {parents} but has no field \"parent\" relating to %s", t.Name, t.Name)
		}
	}
	return nil
}

// TemplateName returns Template or its default, the type name.
func (t Type) TemplateName() string {
	if t.Template != "" {
		return t.Template
	}
	return t.Name
}

// Tabs returns the distinct tabs of the type's fields in declaration order.
func (t Type) Tabs() []string {
	seen := map[string]bool{}
	var tabs []string
	for _, f := range t.Fields {
		if !seen[f.Tab] {
			seen[f.Tab] = true
			tabs = append(tabs, f.Tab)
		}
	}
	return tabs
}

// Storage tells the storage layer how values of a kind are persisted.
type Storage int

const (
	StoreText     Storage = iota // short string column
	StoreLongText                // long string column
	StoreNumber                  // floating point column
	StoreBool                    // boolean column
	StoreGroup                   // one component (own table)
	StoreList                    // ordered components of one table
	StoreBlocks                  // ordered components of block tables
	StoreRelation                // links to entries of another type
)

// Container reports whether values live in component tables.
func (s Storage) Container() bool {
	return s == StoreGroup || s == StoreList || s == StoreBlocks
}

// NormalizeArgs are the arguments of Kind.Normalize. More fields may be
// added; existing kinds keep working.
type NormalizeArgs struct {
	Schema *Schema
	Field  Field
	Raw    any
}

// Kind implements a field type.
type Kind struct {
	Name    string
	Storage Storage
	// Normalize validates a raw (JSON-decoded) value and returns the value to
	// store. A nil Raw value means the field was empty or absent.
	Normalize func(a NormalizeArgs) (any, error)
	// Empty reports whether a normalized value counts as empty for Required.
	Empty func(v any) bool
	// AdminTemplate is the html/template source of the admin input for
	// kinds that are not built in. It receives {F, M, E}: the Field, the
	// Alpine.js expression of the bound value (use x-model="{{.M}}") and
	// the expression of its error path. Label, help and errors are
	// rendered around it.
	AdminTemplate string
}

// Field returns the top-level field with the given name, or nil.
func (t *Type) Field(name string) *Field {
	for i := range t.Fields {
		if t.Fields[i].Name == name {
			return &t.Fields[i]
		}
	}
	return nil
}
