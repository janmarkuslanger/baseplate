// Package content describes content types, fields and blocks, and validates
// entry data against them. Schemas are defined in code by the customer
// project and by plugins.
package content

import (
	"fmt"
	"sort"
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
}

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
}

func (b Block) TemplateName() string {
	if b.Template != "" {
		return b.Template
	}
	return "block/" + b.Name
}

func (t Type) Title() string {
	if t.TitleField != "" {
		return t.TitleField
	}
	return "title"
}

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

// Kind implements a field type.
type Kind struct {
	Name    string
	Storage Storage
	// Normalize validates a raw (JSON-decoded) value and returns the value to
	// store. A nil raw value means the field was empty or absent.
	Normalize func(s *Schema, f Field, raw any) (any, error)
	// Empty reports whether a normalized value counts as empty for Required.
	Empty func(v any) bool
}

// Schema holds all registered kinds, types and blocks.
type Schema struct {
	kinds  map[string]Kind
	types  map[string]*Type
	order  []string
	blocks map[string]Block
}

func NewSchema() *Schema {
	return &Schema{kinds: map[string]Kind{}, types: map[string]*Type{}, blocks: map[string]Block{}}
}

func (s *Schema) AddKind(k Kind) error {
	if k.Name == "" || k.Normalize == nil {
		return fmt.Errorf("content: kind needs a name and Normalize")
	}
	if _, dup := s.kinds[k.Name]; dup {
		return fmt.Errorf("content: kind %q already registered", k.Name)
	}
	s.kinds[k.Name] = k
	return nil
}

func (s *Schema) AddType(t Type) error {
	if t.Name == "" {
		return fmt.Errorf("content: type needs a name")
	}
	if _, dup := s.types[t.Name]; dup {
		return fmt.Errorf("content: type %q already registered", t.Name)
	}
	if t.Label == "" {
		t.Label = t.Name
	}
	if t.LabelPlural == "" {
		t.LabelPlural = t.Label
	}
	s.types[t.Name] = &t
	s.order = append(s.order, t.Name)
	return nil
}

// Extend appends fields to an existing type.
func (s *Schema) Extend(typeName string, fields ...Field) error {
	t, ok := s.types[typeName]
	if !ok {
		return fmt.Errorf("content: cannot extend unknown type %q", typeName)
	}
	for _, f := range fields {
		if t.field(f.Name) != nil {
			return fmt.Errorf("content: type %q already has field %q", typeName, f.Name)
		}
		t.Fields = append(t.Fields, f)
	}
	return nil
}

func (s *Schema) AddBlock(b Block) error {
	if b.Name == "" {
		return fmt.Errorf("content: block needs a name")
	}
	if _, dup := s.blocks[b.Name]; dup {
		return fmt.Errorf("content: block %q already registered", b.Name)
	}
	if b.Label == "" {
		b.Label = b.Name
	}
	s.blocks[b.Name] = b
	return nil
}

// Kind returns a registered kind.
func (s *Schema) Kind(name string) (Kind, bool) {
	k, ok := s.kinds[name]
	return k, ok
}

// AllBlocks returns every registered block, sorted by name.
func (s *Schema) AllBlocks() []Block {
	out := make([]Block, 0, len(s.blocks))
	for _, b := range s.blocks {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Schema) Type(name string) (*Type, bool) {
	t, ok := s.types[name]
	return t, ok
}

// Types returns all types in registration order.
func (s *Schema) Types() []*Type {
	out := make([]*Type, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.types[n])
	}
	return out
}

func (s *Schema) Block(name string) (Block, bool) {
	b, ok := s.blocks[name]
	return b, ok
}

// Blocks returns the blocks allowed by f (all blocks if f.Blocks is empty),
// sorted by label.
func (s *Schema) Blocks(f Field) []Block {
	var out []Block
	if len(f.Blocks) > 0 {
		for _, n := range f.Blocks {
			if b, ok := s.blocks[n]; ok {
				out = append(out, b)
			}
		}
		return out
	}
	for _, b := range s.blocks {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Check verifies that every field of every type and block uses a registered
// kind. Call it once all plugins are registered.
func (s *Schema) Check() error {
	var walk func(owner string, fs []Field) error
	walk = func(owner string, fs []Field) error {
		for _, f := range fs {
			k, ok := s.kinds[f.Kind]
			if !ok {
				return fmt.Errorf("content: %s.%s uses unknown kind %q", owner, f.Name, f.Kind)
			}
			if k.Storage == StoreRelation {
				if _, ok := s.types[f.Ref]; !ok {
					return fmt.Errorf("content: %s.%s relates to unknown type %q", owner, f.Name, f.Ref)
				}
			}
			if err := walk(owner+"."+f.Name, f.Fields); err != nil {
				return err
			}
		}
		return nil
	}
	for _, t := range s.Types() {
		if err := walk(t.Name, t.Fields); err != nil {
			return err
		}
	}
	for _, b := range s.blocks {
		if err := walk("block "+b.Name, b.Fields); err != nil {
			return err
		}
	}
	return nil
}

// Errors maps field paths (e.g. "seo.metaTitle", "body.2.text") to messages.
type Errors map[string]string

func (e Errors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e[k]
	}
	return strings.Join(parts, "; ")
}

// Validate normalizes data against fields. Unknown keys are dropped. It
// returns the cleaned data and, if any field is invalid, an Errors value.
func (s *Schema) Validate(fields []Field, data map[string]any) (map[string]any, error) {
	errs := Errors{}
	out := s.validateFields("", fields, data, errs)
	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

func (s *Schema) validateFields(prefix string, fields []Field, data map[string]any, errs Errors) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		path := prefix + f.Name
		k := s.kinds[f.Kind]
		v, err := k.Normalize(s, f, data[f.Name])
		if err != nil {
			if nested, ok := err.(Errors); ok {
				for p, m := range nested {
					errs[path+"."+p] = m
				}
			} else {
				errs[path] = err.Error()
			}
			continue
		}
		if f.Required && (v == nil || (k.Empty != nil && k.Empty(v))) {
			errs[path] = "Pflichtfeld"
			continue
		}
		if v != nil {
			out[f.Name] = v
		}
	}
	return out
}

// ValidateNested is used by container kinds (group, list, blocks) to
// validate sub-fields. Errors are returned with paths relative to the
// container.
func (s *Schema) ValidateNested(fields []Field, raw any) (map[string]any, error) {
	m, _ := raw.(map[string]any)
	errs := Errors{}
	out := s.validateFields("", fields, m, errs)
	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

func (t *Type) field(name string) *Field {
	for i := range t.Fields {
		if t.Fields[i].Name == name {
			return &t.Fields[i]
		}
	}
	return nil
}
