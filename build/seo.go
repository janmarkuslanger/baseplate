package build

import (
	"cmp"
	"strings"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/settings"
)

func markdownURL(u string) string {
	if u == "/" {
		return "/index.md"
	}
	return strings.TrimSuffix(u, "/") + ".md"
}

func meta(p *render.Page, cfg settings.Settings) (render.Meta, error) {
	seo, _ := p.Data["seo"].(map[string]any)
	title := str(p.Data["title"])
	m := render.Meta{
		Title:       cmp.Or(str(seo["metaTitle"]), title),
		Description: str(seo["metaDescription"]),
		Canonical:   p.AbsURL,
	}
	if c := str(seo["canonical"]); c != "" {
		if !fields.ValidCanonical(c) { // saved before the admin validated it
			return m, i18n.M("build.canonicalInvalid", p.URL, c)
		}
		m.Canonical = absolute(cfg.BaseURL, c)
	}
	if site := cfg.SiteName; site != "" && str(seo["metaTitle"]) == "" && title != site {
		m.Title = title + " | " + site
	}
	robots := []string{"index", "follow"}
	if seo["noindex"] == true {
		robots[0] = "noindex"
	}
	if seo["nofollow"] == true {
		robots[1] = "nofollow"
	}
	m.Robots = strings.Join(robots, ", ")
	m.OGTitle = cmp.Or(str(seo["ogTitle"]), m.Title)
	m.OGDescription = cmp.Or(str(seo["ogDescription"]), m.Description)
	m.OGImage = absolute(cfg.BaseURL, cmp.Or(str(seo["ogImage"]), cfg.DefaultOGImage))
	return m, nil
}

func indexable(p *render.Page) bool {
	seo, _ := p.Data["seo"].(map[string]any)
	return seo["noindex"] != true
}

func absolute(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return base + u
}
