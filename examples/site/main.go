// Example customer project. A real project lives in its own repository
// and requires this module in its go.mod (see README, "Quickstart").
package main

import (
	"embed"
	"io/fs"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/plugins/forms"
)

//go:embed theme
var themeFS embed.FS

func main() {
	theme, _ := fs.Sub(themeFS, "theme")
	app := baseplate.New(baseplate.Config{Theme: theme})
	app.Use(forms.New())
	app.Main()
}
