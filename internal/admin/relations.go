package admin

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

// refOptions lists the choices of every relation picker the editor of t can
// show (also inside groups, lists and blocks), one query per target type.
func (a *Admin) refOptions(ctx context.Context, t *content.Type) map[string][]content.Option {
	targets := map[string]bool{}
	seenBlocks := map[string]bool{}
	var walk func(fs []content.Field)
	walk = func(fs []content.Field) {
		for _, f := range fs {
			if f.Kind == fields.Relation {
				targets[f.Ref] = true
			}
			walk(f.Fields)
			if f.Kind == fields.Blocks {
				for _, b := range a.Reg.Schema.Blocks(f) {
					if !seenBlocks[b.Name] {
						seenBlocks[b.Name] = true
						walk(b.Fields)
					}
				}
			}
		}
	}
	walk(t.Fields)
	out := make(map[string][]content.Option, len(targets))
	for typ := range targets {
		out[typ] = a.entryOptions(ctx, typ)
	}
	return out
}

// entryOptions lists the entries of a type, sorted by title.
func (a *Admin) entryOptions(ctx context.Context, typ string) []content.Option {
	t, ok := a.Reg.Schema.Type(typ)
	if !ok {
		return nil
	}
	list, _, err := a.store().ListEntries(ctx, storage.ListQuery{Type: typ, Version: storage.Draft})
	if err != nil {
		a.Logger.Error("relation options", "type", typ, "err", err)
		return nil
	}
	out := make([]content.Option, 0, len(list))
	for _, e := range list {
		out = append(out, content.Option{Value: strconv.FormatInt(e.ID, 10), Label: fmt.Sprint(a.orDash(e.Data[t.Title()]))})
	}
	slices.SortFunc(out, func(x, y content.Option) int { return cmp.Compare(x.Label, y.Label) })
	return out
}
