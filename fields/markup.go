package fields

import "github.com/janmarkuslanger/baseplate/internal/markup"

// SanitizeHTML reduces untrusted HTML to the rich-text allowlist.
func SanitizeHTML(s string) string { return markup.SanitizeHTML(s) }

// HTMLToMarkdown converts sanitized rich-text HTML to Markdown.
func HTMLToMarkdown(s string) string { return markup.HTMLToMarkdown(s) }

func safeURL(u string) bool { return markup.SafeURL(u) }
