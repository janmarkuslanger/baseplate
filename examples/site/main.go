// Example customer project. A real project lives in its own repository
// and requires this module in its go.mod (see README, "Quickstart").
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugins/forms"
)

//go:embed theme
var themeFS embed.FS

func main() {
	theme, err := fs.Sub(themeFS, "theme")
	if err != nil {
		log.Fatal(err)
	}
	app := baseplate.New(baseplate.Config{Theme: theme})
	app.Use(fields.CoreBlocks{}, forms.New())
	app.Main()
}
