// Package fields provides baseplate's built-in field kinds, blocks and
// content types (page, site settings), plus the standard SEO fields.
package fields

import (
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/janmarkuslanger/baseplate/content"
)

// Kind names.
const (
	Text      = "text"
	Textarea  = "textarea"
	RichText  = "richtext"
	Slug      = "slug"
	Number    = "number"
	Bool      = "bool"
	Select    = "select"
	Image     = "image"
	Email     = "email"
	URL       = "url"
	Reference = "reference"
	Group     = "group"
	List      = "list"
	Blocks    = "blocks"
)

// BlockTypeKey holds the block name inside each item of a blocks field.
const BlockTypeKey = "_type"

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
	return "", errors.New("Text erwartet")
}

func stringKind(name string, check func(f content.Field, s string) (string, error)) content.Kind {
	return content.Kind{
		Name:  name,
		Empty: emptyString,
		Normalize: func(_ *content.Schema, f content.Field, raw any) (any, error) {
			s, err := asString(raw)
			if err != nil {
				return nil, err
			}
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, nil
			}
			if f.Max > 0 && utf8.RuneCountInString(s) > f.Max {
				return nil, fmt.Errorf("maximal %d Zeichen", f.Max)
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
		stringKind(Text, nil),
		stringKind(Textarea, nil),
		stringKind(RichText, func(_ content.Field, s string) (string, error) {
			return SanitizeHTML(s), nil
		}),
		stringKind(Slug, func(_ content.Field, s string) (string, error) {
			s = strings.ToLower(s)
			if !slugRe.MatchString(s) {
				return "", errors.New("nur Kleinbuchstaben, Ziffern und Bindestriche")
			}
			return s, nil
		}),
		stringKind(Select, func(f content.Field, s string) (string, error) {
			for _, o := range f.Options {
				if o.Value == s {
					return s, nil
				}
			}
			return "", errors.New("ungültige Auswahl")
		}),
		stringKind(Image, func(_ content.Field, s string) (string, error) {
			if strings.HasPrefix(s, "/media/") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
				return s, nil
			}
			return "", errors.New("ungültiger Bildpfad")
		}),
		stringKind(Email, func(_ content.Field, s string) (string, error) {
			a, err := mail.ParseAddress(s)
			if err != nil || a.Address != s {
				return "", errors.New("ungültige E-Mail-Adresse")
			}
			return s, nil
		}),
		stringKind(URL, func(_ content.Field, s string) (string, error) {
			if !safeURL(s) {
				return "", errors.New("URL muss mit https://, http://, / oder mailto: beginnen")
			}
			return s, nil
		}),
		{
			Name: Number,
			Normalize: func(_ *content.Schema, _ content.Field, raw any) (any, error) {
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
						return nil, errors.New("Zahl erwartet")
					}
					return n, nil
				}
				return nil, errors.New("Zahl erwartet")
			},
		},
		{
			Name: Bool,
			Normalize: func(_ *content.Schema, _ content.Field, raw any) (any, error) {
				switch v := raw.(type) {
				case nil:
					return false, nil
				case bool:
					return v, nil
				case string:
					return v == "true" || v == "on" || v == "1", nil
				}
				return nil, errors.New("Ja/Nein erwartet")
			},
			Empty: func(v any) bool { b, _ := v.(bool); return !b },
		},
		{
			Name: Reference,
			Normalize: func(_ *content.Schema, _ content.Field, raw any) (any, error) {
				switch v := raw.(type) {
				case nil:
					return nil, nil
				case float64:
					if v <= 0 {
						return nil, nil
					}
					return int64(v), nil
				case string:
					if v == "" {
						return nil, nil
					}
					n, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						return nil, errors.New("ungültige Referenz")
					}
					return n, nil
				}
				return nil, errors.New("ungültige Referenz")
			},
		},
		{
			Name: Group,
			Normalize: func(s *content.Schema, f content.Field, raw any) (any, error) {
				if raw == nil {
					raw = map[string]any{}
				}
				if _, ok := raw.(map[string]any); !ok {
					return nil, errors.New("Objekt erwartet")
				}
				return s.ValidateNested(f.Fields, raw)
			},
		},
		{
			Name:  List,
			Empty: emptySlice,
			Normalize: func(s *content.Schema, f content.Field, raw any) (any, error) {
				items, err := asList(raw)
				if err != nil {
					return nil, err
				}
				out := make([]any, 0, len(items))
				errs := content.Errors{}
				for i, it := range items {
					v, err := s.ValidateNested(f.Fields, it)
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
			Name:  Blocks,
			Empty: emptySlice,
			Normalize: func(s *content.Schema, f content.Field, raw any) (any, error) {
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
						errs[strconv.Itoa(i)] = fmt.Sprintf("unbekanntes Element %q", name)
						continue
					}
					v, err := s.ValidateNested(b.Fields, m)
					collect(errs, i, err)
					v[BlockTypeKey] = name
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

func asList(raw any) ([]any, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("Liste erwartet")
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
	errs[strconv.Itoa(i)] = err.Error()
}
