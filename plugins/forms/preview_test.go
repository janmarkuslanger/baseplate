package forms

import (
	"context"
	"testing"
)

// The submissions preview leaves out checkbox values in any site language,
// but keeps a text answer that happens to be "ja".
func TestSubmissionPreviewSkipsCheckboxes(t *testing.T) {
	reg, p, _, id := setup(t)
	ctx := context.Background()
	e, err := reg.Store.GetEntry(ctx, TypeName, id)
	if err != nil {
		t.Fatal(err)
	}
	e.Data["fields"] = append(e.Data["fields"].([]any), map[string]any{"label": "Rückruf?", "name": "rueckruf", "type": "text"})
	if err := reg.Store.UpdateEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	s := p.label(rawSubmission{formID: id, data: `{"name":"Ada","dsgvo":"yes","rueckruf":"ja"}`}, map[int64]string{id: "Kontakt"})
	if s.Preview != "Ada · ja" {
		t.Errorf("preview = %q, want %q", s.Preview, "Ada · ja")
	}
}
