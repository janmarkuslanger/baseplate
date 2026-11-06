package plugin_test

import (
	"fmt"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
)

type News struct{}

func (News) Name() string { return "news" }

func (News) Register(r *plugin.Registry) error {
	// A new content type with the standard title/slug/SEO fields.
	// It appears in the admin navigation automatically.
	return r.ContentType(content.Type{
		Name: "news", Label: "News", LabelPlural: "News", Routable: true,
		Fields: fields.RoutableFields(
			content.Field{Name: "teaser", Label: "Teaser", Kind: fields.Textarea},
		),
	})
}

// A plugin that adds a content type, as shown in the README.
func Example() {
	reg := plugin.NewRegistry(plugin.Services{})
	if err := reg.Use(fields.Core{}, News{}); err != nil {
		fmt.Println(err)
		return
	}
	if err := reg.Schema.Check(); err != nil {
		fmt.Println(err)
		return
	}
	news, _ := reg.Schema.Type("news")
	fmt.Println(reg.Plugins(), news.Routable)
	// Output: [core news] true
}
