// Package fields provides baseplate's built-in field kinds, blocks and
// content types (page, site settings), plus the standard SEO fields.
package fields

import (
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zones also on hosts without zoneinfo
	"unicode/utf8"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/i18n"
)

// Kind names.
const (
	Text     = "text"
	Textarea = "textarea"
	RichText = "richtext"
	Slug     = "slug"
	Number   = "number"
	Bool     = "bool"
	Select   = "select"
	Image    = "image"
	Email    = "email"
	URL      = "url"
	Relation = "relation"
	Group    = "group"
	List     = "list"
	Blocks   = "blocks"
	// Date and DateTime store Unix seconds; the admin converts input in the
	// site's time zone, templates format with {{date v "long"}}.
	Date     = "date"
	DateTime = "datetime"
	// Timezone is an IANA zone name ("Europe/Berlin").
	Timezone = "timezone"
)

// BlockTypeKey and ItemKey are aliases of the content conventions, kept for
// plugins that import them from here.
const BlockTypeKey = content.BlockTypeKey

const ItemKey = content.ItemKey

var itemKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*:[0-9]+$`)

// keepKey copies a valid item key from raw to out.
func keepKey(out map[string]any, raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		if k, ok := m[ItemKey].(string); ok && itemKeyRe.MatchString(k) {
			out[ItemKey] = k
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func emptyString(v any) bool { s, _ := v.(string); return s == "" }
func emptySlice(v any) bool  { s, _ := v.([]any); return len(s) == 0 }

func asString(raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	}
	return "", i18n.M("validation.text")
}

func stringKind(name string, st content.Storage, check func(f content.Field, s string) (string, error)) content.Kind {
	return content.Kind{
		Name:    name,
		Storage: st,
		Empty:   emptyString,
		Normalize: func(a content.NormalizeArgs) (any, error) {
			f, raw := a.Field, a.Raw
			s, err := asString(raw)
			if err != nil {
				return nil, err
			}
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, nil
			}
			if f.Max > 0 && utf8.RuneCountInString(s) > f.Max {
				return nil, i18n.M("validation.maxLength", f.Max)
			}
			if check != nil {
				return check(f, s)
			}
			return s, nil
		},
	}
}

// Kinds returns all built-in kinds.
func Kinds() []content.Kind {
	return []content.Kind{
		stringKind(Text, content.StoreText, nil),
		stringKind(Textarea, content.StoreLongText, nil),
		stringKind(RichText, content.StoreLongText, func(_ content.Field, s string) (string, error) {
			return SanitizeHTML(s), nil
		}),
		stringKind(Slug, content.StoreText, func(_ content.Field, s string) (string, error) {
			s = strings.ToLower(s)
			if !slugRe.MatchString(s) {
				return "", i18n.M("validation.slug")
			}
			return s, nil
		}),
		stringKind(Select, content.StoreText, func(f content.Field, s string) (string, error) {
			for _, o := range f.Options {
				if o.Value == s {
					return s, nil
				}
			}
			return "", i18n.M("validation.select")
		}),
		stringKind(Image, content.StoreText, func(_ content.Field, s string) (string, error) {
			if strings.HasPrefix(s, "/media/") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
				return s, nil
			}
			return "", i18n.M("validation.image")
		}),
		stringKind(Email, content.StoreText, func(_ content.Field, s string) (string, error) {
			a, err := mail.ParseAddress(s)
			if err != nil || a.Address != s {
				return "", i18n.M("validation.email")
			}
			return s, nil
		}),
		stringKind(URL, content.StoreText, func(_ content.Field, s string) (string, error) {
			if !safeURL(s) {
				return "", i18n.M("validation.url")
			}
			return s, nil
		}),
		{
			Name:    Number,
			Storage: content.StoreNumber,
			Normalize: func(a content.NormalizeArgs) (any, error) {
				raw := a.Raw
				switch v := raw.(type) {
				case nil:
					return nil, nil
				case float64:
					return v, nil
				case string:
					if strings.TrimSpace(v) == "" {
						return nil, nil
					}
					n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
					if err != nil {
						return nil, i18n.M("validation.number")
					}
					return n, nil
				}
				return nil, i18n.M("validation.number")
			},
		},
		timeKind(Date),
		timeKind(DateTime),
		stringKind(Timezone, content.StoreText, func(_ content.Field, s string) (string, error) {
			if _, err := time.LoadLocation(s); err != nil || s == "Local" {
				return "", i18n.M("validation.timezone")
			}
			return s, nil
		}),
		{
			Name:    Bool,
			Storage: content.StoreBool,
			Normalize: func(a content.NormalizeArgs) (any, error) {
				raw := a.Raw
				switch v := raw.(type) {
				case nil:
					return false, nil
				case bool:
					return v, nil
				case string:
					return v == "true" || v == "on" || v == "1", nil
				}
				return nil, i18n.M("validation.bool")
			},
			Empty: func(v any) bool { b, _ := v.(bool); return !b },
		},
		{
			Name:    Relation,
			Storage: content.StoreRelation,
			Empty:   func(v any) bool { l, ok := v.([]any); return v == nil || (ok && len(l) == 0) },
			Normalize: func(a content.NormalizeArgs) (any, error) {
				f, raw := a.Field, a.Raw
				if !f.Many {
					return refID(raw)
				}
				items, err := asList(raw)
				if err != nil {
					return nil, err
				}
				seen := map[int64]bool{}
				out := []any{}
				for _, it := range items {
					id, err := refID(it)
					if err != nil {
						return nil, err
					}
					if id, ok := id.(int64); ok && !seen[id] {
						seen[id] = true
						out = append(out, id)
					}
				}
				return out, nil
			},
		},
		{
			Name:    Group,
			Storage: content.StoreGroup,
			Normalize: func(a content.NormalizeArgs) (any, error) {
				s, f, raw := a.Schema, a.Field, a.Raw
				if raw == nil {
					raw = map[string]any{}
				}
				if _, ok := raw.(map[string]any); !ok {
					return nil, i18n.M("validation.object")
				}
				v, err := s.ValidateNested(f.Fields, raw)
				return keepKey(v, raw), err
			},
		},
		{
			Name:    List,
			Storage: content.StoreList,
			Empty:   emptySlice,
			Normalize: func(a content.NormalizeArgs) (any, error) {
				s, f, raw := a.Schema, a.Field, a.Raw
				items, err := asList(raw)
				if err != nil {
					return nil, err
				}
				out := make([]any, 0, len(items))
				errs := content.Errors{}
				for i, it := range items {
					v, err := s.ValidateNested(f.Fields, it)
					keepKey(v, it)
					collect(errs, i, err)
					out = append(out, v)
				}
				if len(errs) > 0 {
					return nil, errs
				}
				return out, nil
			},
		},
		{
			Name:    Blocks,
			Storage: content.StoreBlocks,
			Empty:   emptySlice,
			Normalize: func(a content.NormalizeArgs) (any, error) {
				s, f, raw := a.Schema, a.Field, a.Raw
				items, err := asList(raw)
				if err != nil {
					return nil, err
				}
				allowed := map[string]bool{}
				for _, b := range s.Blocks(f) {
					allowed[b.Name] = true
				}
				out := make([]any, 0, len(items))
				errs := content.Errors{}
				for i, it := range items {
					m, _ := it.(map[string]any)
					name, _ := m[BlockTypeKey].(string)
					b, ok := s.Block(name)
					if !ok || !allowed[name] {
						errs[strconv.Itoa(i)] = i18n.M("validation.unknownBlock", name)
						continue
					}
					v, err := s.ValidateNested(b.Fields, m)
					collect(errs, i, err)
					v[BlockTypeKey] = name
					keepKey(v, m)
					out = append(out, v)
				}
				if len(errs) > 0 {
					return nil, errs
				}
				return out, nil
			},
		},
	}
}

// refID normalizes a relation target: empty, zero or negative mean "none";
// anything else that is not an ID is invalid.
func refID(raw any) (any, error) {
	if raw == nil || raw == "" {
		return nil, nil
	}
	if id, ok := content.RefID(raw); ok {
		return id, nil
	}
	switch raw.(type) {
	case int64, int, float64:
		return nil, nil
	}
	return nil, i18n.M("validation.relation")
}

func asList(raw any) ([]any, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, i18n.M("validation.list")
	}
	return items, nil
}

func collect(errs content.Errors, i int, err error) {
	if err == nil {
		return
	}
	if nested, ok := err.(content.Errors); ok {
		for p, m := range nested {
			errs[strconv.Itoa(i)+"."+p] = m
		}
		return
	}
	errs[strconv.Itoa(i)] = err
}

// timeKind stores a point in time as Unix seconds. Input: a number (the
// admin sends seconds) or an RFC 3339 string with offset (imports, APIs).
func timeKind(name string) content.Kind {
	return content.Kind{
		Name:    name,
		Storage: content.StoreNumber,
		Normalize: func(a content.NormalizeArgs) (any, error) {
			switch v := a.Raw.(type) {
			case nil:
				return nil, nil
			case float64:
				return int64(v), nil
			case int64:
				return v, nil
			case int:
				return int64(v), nil
			case string:
				if strings.TrimSpace(v) == "" {
					return nil, nil
				}
				t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
				if err != nil {
					return nil, i18n.M("validation.date")
				}
				return t.Unix(), nil
			}
			return nil, i18n.M("validation.date")
		},
	}
}
