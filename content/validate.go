package content

import (
	"errors"
	"slices"
	"strings"

	"github.com/janmarkuslanger/baseplate/i18n"
)

// Errors maps field paths (e.g. "seo.metaTitle", "body.2.text") to errors.
// Validation errors are i18n.Msg values, translated where they are shown.
type Errors map[string]error

func (e Errors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e[k].Error()
	}
	return strings.Join(parts, "; ")
}

// Validate normalizes data against fields. Unknown keys are dropped. It
// returns the cleaned data and, if any field is invalid, an Errors value.
func (s *Schema) Validate(fields []Field, data map[string]any) (map[string]any, error) {
	errs := Errors{}
	out := s.validateFields("", fields, data, errs)
	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

func (s *Schema) validateFields(prefix string, fields []Field, data map[string]any, errs Errors) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		path := prefix + f.Name
		k := s.kinds[f.Kind]
		v, err := k.Normalize(NormalizeArgs{Schema: s, Field: f, Raw: data[f.Name]})
		if err != nil {
			if nested, ok := errors.AsType[Errors](err); ok {
				for p, m := range nested {
					errs[path+"."+p] = m
				}
			} else {
				errs[path] = err
			}
			continue
		}
		if f.Required && (v == nil || (k.Empty != nil && k.Empty(v))) {
			errs[path] = i18n.M("validation.required")
			continue
		}
		if v != nil {
			out[f.Name] = v
		}
	}
	return out
}

// ValidateNested is used by container kinds (group, list, blocks) to
// validate sub-fields. Errors are returned with paths relative to the
// container.
func (s *Schema) ValidateNested(fields []Field, raw any) (map[string]any, error) {
	m, _ := raw.(map[string]any)
	errs := Errors{}
	out := s.validateFields("", fields, m, errs)
	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}
