package render

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/plugin"
)

type buildKey struct{}

// Plugin functions taking a context get the one of the Renderer they run
// in, so concurrent builds and previews each use their own.
func TestPluginFuncsGetTheRenderersContext(t *testing.T) {
	reg := plugin.NewRegistry(plugin.Services{})
	if err := reg.Funcs(plugin.TemplateFuncs{
		"build": func(ctx context.Context, prefix string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return prefix + ctx.Value(buildKey{}).(string), nil
		},
		"join": func(ctx context.Context, parts ...string) string {
			return strings.Join(parts, ctx.Value(buildKey{}).(string))
		},
		"shout": strings.ToUpper,
	}); err != nil {
		t.Fatal(err)
	}
	theme := fstest.MapFS{"templates/t.html": {Data: []byte(`{{build "#"}} {{join "a" "b"}} {{shout "x"}}`)}}
	render := func(ctx context.Context) (string, error) {
		r, err := New(reg, theme, Options{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		err = r.Execute(&b, "t.html", nil)
		return b.String(), err
	}

	a := context.WithValue(context.Background(), buildKey{}, "A")
	b := context.WithValue(context.Background(), buildKey{}, "B")
	for ctx, want := range map[context.Context]string{a: "#A aAb X", b: "#B aBb X"} {
		if got, err := render(ctx); err != nil || got != want {
			t.Errorf("got %q, %v, want %q", got, err, want)
		}
	}

	cancelled, cancel := context.WithCancel(a)
	cancel()
	if _, err := render(cancelled); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("cancelled context: err = %v, want %v", err, context.Canceled)
	}
}
