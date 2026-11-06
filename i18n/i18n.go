// Package i18n translates the admin interface. Texts are addressed by
// keys ("entry.save"); catalogs map keys to texts per language. Texts may
// contain placeholders {0}, {1}, … for arguments.
//
// A key without a translation is returned unchanged, so plain texts (e.g.
// labels in plugins without catalogs) still render.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/janmarkuslanger/baseplate/hook"
)

// Default is the fallback language.
const Default = "de"

//go:embed locales/*.json
var coreFS embed.FS

// Catalog is a set of translations for one language.
type Catalog struct {
	Lang     string
	Messages map[string]string
}

// Catalogs lets plugins contribute translations:
//
//	i18n.Catalogs.Add(r, i18n.Catalog{Lang: "de", Messages: map[string]string{"booking.title": "Termine"}})
var Catalogs = hook.NewList[Catalog]("i18n.catalogs", "translations contributed by plugins")

// Core returns baseplate's own catalogs.
func Core() []Catalog {
	entries, _ := coreFS.ReadDir("locales")
	var out []Catalog
	for _, e := range entries {
		b, err := coreFS.ReadFile(path.Join("locales", e.Name()))
		if err != nil {
			panic(err)
		}
		c := Catalog{Lang: strings.TrimSuffix(e.Name(), ".json")}
		if err := json.Unmarshal(b, &c.Messages); err != nil {
			panic(fmt.Sprintf("i18n: %s: %v", e.Name(), err))
		}
		out = append(out, c)
	}
	return out
}

// Bundle merges catalogs; later catalogs override earlier keys.
type Bundle struct{ langs map[string]map[string]string }

func NewBundle(cs ...Catalog) *Bundle {
	b := &Bundle{langs: map[string]map[string]string{}}
	for _, c := range cs {
		b.Add(c)
	}
	return b
}

func (b *Bundle) Add(c Catalog) {
	m := b.langs[c.Lang]
	if m == nil {
		m = map[string]string{}
		b.langs[c.Lang] = m
	}
	for k, v := range c.Messages {
		m[k] = v
	}
}

// Has reports whether lang has a text for key.
func (b *Bundle) Has(lang, key string) bool { _, ok := b.langs[lang][key]; return ok }

// Langs lists the available languages.
func (b *Bundle) Langs() []string {
	out := make([]string, 0, len(b.langs))
	for l := range b.langs {
		out = append(out, l)
	}
	return out
}

// T translates key into lang (falling back to Default, then the key).
func (b *Bundle) T(lang, key string, args ...any) string {
	s, ok := b.langs[lang][key]
	if !ok {
		s, ok = b.langs[Default][key]
	}
	if !ok {
		s = key
	}
	for i, a := range args {
		s = strings.ReplaceAll(s, "{"+strconv.Itoa(i)+"}", fmt.Sprint(a))
	}
	return s
}

// Messages returns all texts of lang whose keys start with prefix (with
// Default as fallback), e.g. for client-side scripts.
func (b *Bundle) Messages(lang, prefix string) map[string]string {
	out := map[string]string{}
	for _, l := range []string{Default, lang} {
		for k, v := range b.langs[l] {
			if strings.HasPrefix(k, prefix) {
				out[k] = v
			}
		}
	}
	return out
}

// Msg is a translatable error: validation and other user-facing errors
// carry a key and arguments, and are translated where they are shown.
type Msg struct {
	Key  string
	Args []any
}

// M builds a Msg.
func M(key string, args ...any) Msg { return Msg{Key: key, Args: args} }

var core = NewBundle(Core()...)

// Error returns the text in the Default language, for logs.
func (m Msg) Error() string { return core.T(Default, m.Key, m.Args...) }

// Key is a translatable sentinel error: comparable, so errors.Is works.
//
//	var ErrInvalidLogin = i18n.Key("login.invalid")
type Key string

// Error returns the text in the Default language, for logs.
func (k Key) Error() string { return core.T(Default, string(k)) }

// Tr translates an error for display: the first Msg or Key in its chain
// (also when wrapped), otherwise the error text.
func (b *Bundle) Tr(lang string, err error) string {
	var m Msg
	if errors.As(err, &m) {
		return b.T(lang, m.Key, m.Args...)
	}
	var k Key
	if errors.As(err, &k) {
		return b.T(lang, string(k))
	}
	return err.Error()
}
