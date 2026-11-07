package entries_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/storage"
)

// PublishedChanged fires for every save or delete that touches published
// content, and only for those.
func TestPublishedChangedHook(t *testing.T) {
	svc, reg := service(t)
	ctx := context.Background()
	var got []string
	entries.PublishedChanged.On(reg, func(_ context.Context, e *entries.Event) error {
		got = append(got, e.Type.Name+":"+e.Entry.Slug+":"+e.Entry.Status)
		return errors.New("logged, not returned")
	})

	draft, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("draft", 0)})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("live", 0), Publish: true})
	if err != nil {
		t.Fatalf("a hook error must not fail the save: %v", err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: pub.Entry.ID, Data: page("live", 0)}); err != nil { // unpublish
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, "page", draft.Entry.ID); err != nil {
		t.Fatal(err)
	}
	again, err := svc.Save(ctx, entries.Save{Type: "page", ID: pub.Entry.ID, Data: page("live", 0), Publish: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, "page", again.Entry.ID); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"page:live:" + storage.StatusPublished, // publish
		"page:live:" + storage.StatusDraft,     // unpublish
		"page:live:" + storage.StatusPublished, // publish again
		"page:live:" + storage.StatusPublished, // delete while published
	}
	if !slices.Equal(got, want) {
		t.Errorf("hook calls\n got %v\nwant %v", got, want)
	}
}
