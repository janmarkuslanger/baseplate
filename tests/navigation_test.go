package tests

import (
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/plugins/navigation"
)

// Items are edited in place in the navigation editor: the editor has a form
// template for the item block (with the item's own items field) and offers
// to add items; the page editor does not offer the item block.
func TestNavigationAdmin(t *testing.T) {
	_, c := newTestApp(t, navigation.New())
	c.login()

	_, html := c.do("GET", "/admin/c/"+navigation.TypeName+"/new", nil)
	form, tmpl := splitEditor(html, navigation.ItemBlock)
	if tmpl == "" {
		t.Fatal("navigation editor has no form template for items")
	}
	for _, label := range []string{"Beschriftung", "URL", "Seite", "Items"} {
		if !strings.Contains(tmpl, label) {
			t.Errorf("item form lacks field %q", label)
		}
	}
	add := "newBlock('" + navigation.ItemBlock + "')"
	if !strings.Contains(form, add) {
		t.Error("navigation editor does not offer to add items")
	}
	if !strings.Contains(tmpl, add) {
		t.Error("item form does not offer to add items to the item")
	}

	_, html = c.do("GET", "/admin/c/page/new", nil)
	if form, _ := splitEditor(html, navigation.ItemBlock); strings.Contains(form, add) {
		t.Error("page editor offers the navigation item block")
	}
}

// splitEditor splits an editor page into the form (everything before the
// block form templates) and the form template of block.
func splitEditor(html, block string) (form, tmpl string) {
	const marker = `<template id="bp-block-`
	form, rest, _ := strings.Cut(html, marker)
	for _, part := range strings.Split(marker+rest, marker)[1:] {
		if strings.HasPrefix(part, block+`">`) {
			tmpl = part
		}
	}
	return form, tmpl
}
