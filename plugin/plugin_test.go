package plugin

import (
	"strings"
	"testing"
)

type funcsPlugin struct {
	name  string
	funcs TemplateFuncs
}

func (p funcsPlugin) Name() string               { return p.name }
func (p funcsPlugin) Register(r *Registry) error { return r.Funcs(p.funcs) }

func TestFuncsRegisteredTwice(t *testing.T) {
	r := NewRegistry(Services{})
	a := funcsPlugin{"a", TemplateFuncs{"shout": strings.ToUpper, "trim": strings.TrimSpace}}
	b := funcsPlugin{"b", TemplateFuncs{"shout": strings.ToLower}}
	if err := r.Use(a); err != nil {
		t.Fatal(err)
	}
	err := r.Use(b)
	if err == nil || !strings.Contains(err.Error(), `"shout" registered twice`) {
		t.Fatalf("want duplicate error, got %v", err)
	}
	if f := r.TemplateFuncs()["shout"].(func(string) string); f("x") != "X" {
		t.Error("first registration was overwritten")
	}
}

func TestFuncsDuplicateAddsNothing(t *testing.T) {
	r := NewRegistry(Services{})
	if err := r.Funcs(TemplateFuncs{"a": strings.ToUpper}); err != nil {
		t.Fatal(err)
	}
	if err := r.Funcs(TemplateFuncs{"a": strings.ToLower, "b": strings.ToLower}); err == nil {
		t.Fatal("want error")
	}
	if _, ok := r.TemplateFuncs()["b"]; ok {
		t.Error("a failed call must not add any function")
	}
}
