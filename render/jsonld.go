package render

import "github.com/janmarkuslanger/baseplate/hook"

// Graph is the structured data of one page: the nodes of its JSON-LD
// @graph. The core adds WebSite and the site's organization to the home
// page and a BreadcrumbList to pages below another page.
type Graph struct {
	Page  *Page
	Nodes []map[string]any
}

// JSONLD lets plugins add to or change the structured data of a page before
// it is rendered, e.g. refine the organization to a LocalBusiness. Page.Site
// holds the settings, Page.URL tells the home page ("/") from the others. An
// error is logged and the graph stays as the last function left it.
var JSONLD = hook.NewFilter[*Graph]("render.jsonld", "structured data (JSON-LD) of a page before it is rendered; errors are logged")
