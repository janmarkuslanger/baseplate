package storage

import "testing"

func TestNaming(t *testing.T) {
	for in, want := range map[string]string{"cb_faq": "faq", "cb_faq__items": "faq", "cb_faq__items__x": "faq", "ct_page": "", "ct_page__seo": ""} {
		if got := blockOf(in); got != want {
			t.Errorf("blockOf(%q) = %q, want %q", in, got, want)
		}
	}
	if Column("metaTitle") != "meta_title" || ComponentTable(EntryTable("page"), "seoBox") != "ct_page__seo_box" || blockName(BlockTable("faq")) != "faq" {
		t.Error("naming helpers")
	}
}
