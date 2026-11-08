package entries_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/janmarkuslanger/baseplate/entries"
)

// PublishedChanged fires for every publish, unpublish and delete of a
// published entry, and only for those: saving a draft leaves the site as
// it is.
func TestPublishedChangedHook(t *testing.T) {
	svc, reg := service(t)
	ctx := context.Background()
	var got []string
	entries.PublishedChanged.On(reg, func(_ context.Context, e *entries.Event) error {
		got = append(got, fmt.Sprintf("%s:%s:%v", e.Type.Name, e.Entry.Slug, e.Entry.Published()))
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
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: pub.Entry.ID, Data: page("live", 0)}); err != nil { // draft edit
		t.Fatal(err)
	}
	if _, err := svc.Unpublish(ctx, "page", pub.Entry.ID, ""); err != nil {
		t.Fatal(err)
	}
	if changed, err := svc.Unpublish(ctx, "page", pub.Entry.ID, ""); err != nil || changed {
		t.Errorf("unpublishing an unpublished entry: changed %v, %v", changed, err)
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
		"page:live:true",  // publish
		"page:live:false", // unpublish
		"page:live:true",  // publish again
		"page:live:true",  // delete while published
	}
	if !slices.Equal(got, want) {
		t.Errorf("hook calls\n got %v\nwant %v", got, want)
	}
}
