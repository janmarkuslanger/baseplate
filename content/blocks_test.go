package content

import (
	"slices"
	"testing"
)

func TestBlocksRestricted(t *testing.T) {
	s := NewSchema()
	for _, b := range []Block{{Name: "text", Label: "Text"}, {Name: "link", Label: "Link", Restricted: true}} {
		if err := s.AddBlock(b); err != nil {
			t.Fatal(err)
		}
	}
	names := func(bs []Block) []string {
		var out []string
		for _, b := range bs {
			out = append(out, b.Name)
		}
		return out
	}
	if got := names(s.Blocks(Field{})); !slices.Equal(got, []string{"text"}) {
		t.Errorf("open field offers %v, want only the unrestricted blocks", got)
	}
	if got := names(s.Blocks(Field{Blocks: []string{"link"}})); !slices.Equal(got, []string{"link"}) {
		t.Errorf("field naming the block offers %v, want [link]", got)
	}
	if got := names(s.AllBlocks()); !slices.Equal(got, []string{"link", "text"}) {
		t.Errorf("AllBlocks = %v, want both", got)
	}
}
