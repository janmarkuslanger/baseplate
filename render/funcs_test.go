package render

import (
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/plugin"
)

func TestPluginFuncCollidesWithCore(t *testing.T) {
	for _, name := range []string{"date", "replace"} { // page template func, output template func
		reg := plugin.NewRegistry(plugin.Services{})
		if err := reg.Funcs(plugin.TemplateFuncs{name: strings.ToUpper}); err != nil {
			t.Fatal(err)
		}
		_, err := New(reg, nil, Options{})
		if err == nil || !strings.Contains(err.Error(), `"`+name+`"`) {
			t.Errorf("%s: want collision error, got %v", name, err)
		}
	}
}

func TestPluginFuncWithFreeName(t *testing.T) {
	reg := plugin.NewRegistry(plugin.Services{})
	if err := reg.Funcs(plugin.TemplateFuncs{"shout": strings.ToUpper}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(reg, nil, Options{}); err != nil {
		t.Fatal(err)
	}
}
