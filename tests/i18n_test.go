package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/i18n"
)

// keyUses finds translation keys in templates and Go code.
var keyUses = []*regexp.Regexp{
	regexp.MustCompile(`\{\{-?\s*(?:json \()?t "([a-zA-Z][\w.]*)"`),               // {{t "key"}}, {{json (t "key")}}
	regexp.MustCompile(`\(t "([a-zA-Z][\w.]*)"`),                                  // (t "key" …) inside other calls
	regexp.MustCompile(`\b(?:i18n\.M|a\.t|p\.pt\(lang,)\s*\(?"([a-zA-Z][\w.]*)"`), // Go
	regexp.MustCompile(`(?:Label|LabelPlural|Help|Tab): "([a-z]+\.[\w.]+)"`),      // schema labels that are keys
	regexp.MustCompile(`tr\('(js\.[\w.]+)'`),                                      // admin.js
	regexp.MustCompile(`formsT [^"]*"([\w.]+)"`),                                  // frontend templates
	regexp.MustCompile(`errorKeys[^}]*?"([\w.]+)"`),
}

func TestAllKeysTranslated(t *testing.T) {
	de := map[string]string{}
	for _, c := range i18n.Core() {
		if c.Lang == "de" {
			for k, v := range c.Messages {
				de[k] = v
			}
		}
	}
	b, err := os.ReadFile("../plugins/forms/locales/de.json")
	if err != nil {
		t.Fatal(err)
	}
	var forms map[string]string
	json.Unmarshal(b, &forms)
	for k, v := range forms {
		de[k] = v
	}

	used := map[string]string{}
	filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == ".git" || d.Name() == "examples") {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return err
		}
		ext := filepath.Ext(p)
		if d.IsDir() || strings.HasSuffix(p, "_test.go") || (ext != ".go" && ext != ".html" && ext != ".js") || strings.HasSuffix(p, ".min.js") {
			return nil
		}
		src, _ := os.ReadFile(p)
		for _, re := range keyUses {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				if !strings.HasSuffix(m[1], ".") { // prefixes of runtime keys
					used[m[1]] = p
				}
			}
		}
		return nil
	})
	// Keys built at runtime.
	for _, k := range []string{"flash.saved", "flash.published", "flash.deleted"} {
		used[k] = "admin/admin.go (flash)"
	}
	if len(used) < 100 {
		t.Fatalf("only %d keys found – scanner broken?", len(used))
	}
	var missing []string
	for k, p := range used {
		if _, ok := de[k]; !ok {
			missing = append(missing, k+"  ("+p+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d keys missing in de catalogs:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
