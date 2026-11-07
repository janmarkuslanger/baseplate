package content

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Schema holds all registered kinds, types and blocks.
type Schema struct {
	kinds  map[string]Kind
	types  map[string]*Type
	order  []string
	blocks map[string]Block
}

// NewSchema returns an empty schema.
func NewSchema() *Schema {
	return &Schema{kinds: map[string]Kind{}, types: map[string]*Type{}, blocks: map[string]Block{}}
}

// AddKind registers a field kind; names are unique.
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

// AddType registers a content type; names are unique.
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
		if t.Field(f.Name) != nil {
			return fmt.Errorf("content: type %q already has field %q", typeName, f.Name)
		}
		t.Fields = append(t.Fields, f)
	}
	return nil
}

// AllowBlocks restricts a blocks field to the named blocks. path names the
// field, with dots for fields inside groups (e.g. "body" or "hero.blocks").
// Names are checked by Check, so blocks may be registered later.
func (s *Schema) AllowBlocks(typeName, path string, blocks ...string) error {
	t, ok := s.types[typeName]
	if !ok {
		return fmt.Errorf("content: AllowBlocks: unknown type %q", typeName)
	}
	fs := t.Fields
	parts := strings.Split(path, ".")
	for i, name := range parts {
		var f *Field
		for j := range fs {
			if fs[j].Name == name {
				f = &fs[j]
			}
		}
		if f == nil {
			return fmt.Errorf("content: AllowBlocks: %s has no field %q", typeName, path)
		}
		if i == len(parts)-1 {
			if k, ok := s.kinds[f.Kind]; ok && k.Storage != StoreBlocks {
				return fmt.Errorf("content: AllowBlocks: %s.%s is not a blocks field", typeName, path)
			}
			f.Blocks = append([]string(nil), blocks...)
			return nil
		}
		fs = f.Fields
	}
	return nil
}

// AddBlock registers a block; names are unique.
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

// Kinds returns all registered kinds, sorted by name.
func (s *Schema) Kinds() []Kind {
	out := make([]Kind, 0, len(s.kinds))
	for _, k := range s.kinds {
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b Kind) int { return cmp.Compare(a.Name, b.Name) })
	return out
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
	slices.SortFunc(out, func(a, b Block) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// Type returns a registered content type.
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

// Block returns a registered block.
func (s *Schema) Block(name string) (Block, bool) {
	b, ok := s.blocks[name]
	return b, ok
}

// Blocks returns the blocks allowed by f: the named ones, or all blocks
// except restricted ones if f.Blocks is empty (sorted by label).
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
		if !b.Restricted {
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b Block) int { return cmp.Compare(a.Label, b.Label) })
	return out
}

// Check verifies that every field of every type and block uses a registered
// kind. Call it once all plugins are registered.
func (s *Schema) Check() error {
	var walk func(owner string, fs []Field) error
	walk = func(owner string, fs []Field) error {
		if err := checkRenames(owner, fs); err != nil {
			return err
		}
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
			for _, b := range f.Blocks {
				if _, ok := s.blocks[b]; !ok {
					return fmt.Errorf("content: %s.%s allows unknown block %q", owner, f.Name, b)
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
		if t.Routable {
			if err := t.checkPath(); err != nil {
				return err
			}
		}
	}
	for _, b := range s.blocks {
		if err := walk("block "+b.Name, b.Fields); err != nil {
			return err
		}
	}
	return nil
}

// checkRenames rejects RenamedFrom values that name another field of the
// same list or that two fields share: the store could not tell whose data
// is whose.
func checkRenames(owner string, fs []Field) error {
	names := map[string]bool{}
	for _, f := range fs {
		names[f.Name] = true
	}
	from := map[string]string{}
	for _, f := range fs {
		if f.RenamedFrom == "" || f.RenamedFrom == f.Name {
			continue
		}
		if names[f.RenamedFrom] {
			return fmt.Errorf("content: %s.%s is renamed from %q, which is still a field; remove RenamedFrom before reusing the old name", owner, f.Name, f.RenamedFrom)
		}
		if other, dup := from[f.RenamedFrom]; dup {
			return fmt.Errorf("content: %s.%s and %s.%s are both renamed from %q", owner, other, owner, f.Name, f.RenamedFrom)
		}
		from[f.RenamedFrom] = f.Name
	}
	return nil
}
