package plugin

import (
	"os"
	"strings"
	"testing"
)

// TestReadmePluginExample keeps the plugin snippet in the README identical
// to the compiled one in example_test.go.
func TestReadmePluginExample(t *testing.T) {
	src, err := os.ReadFile("example_test.go")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "type News struct{}")
	register := strings.Index(s, "func (News) Register")
	if start < 0 || register < start {
		t.Fatal("News plugin not found in example_test.go")
	}
	end := register + strings.Index(s[register:], "\n}\n") + len("\n}")
	if snippet := s[start:end]; !strings.Contains(string(readme), snippet) {
		t.Errorf("README.md does not contain the plugin example from example_test.go:\n%s", snippet)
	}
}
