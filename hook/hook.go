// Package hook is baseplate's extension mechanism. Every area (core or
// plugin) declares its extension points as package-level, typed variables:
//
//	var Submitted = hook.NewAction[*Submission]("forms.submitted", "after a submission was stored")
//	var FieldTypes = hook.NewList[FieldType]("forms.fieldTypes", "form field types")
//	var BeforeSend = hook.NewFilter[*Message]("mail.beforeSend", "before a mail is sent")
//
// Plugins attach to them in Register:
//
//	forms.Submitted.On(r, func(ctx context.Context, s *forms.Submission) error { ... })
//
// Rules:
//   - Payloads are structs (usually pointers); fields can be added later
//     without breaking plugins.
//   - Functions run in registration order (the order of app.Use).
//   - The declaring area documents what an error does (abort or log).
//
// Registrations are stored per app in a Set, so several apps (e.g. in
// tests) do not share them.
package hook

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
)

// Set holds the registrations of one app.
type Set struct {
	mu sync.RWMutex
	m  map[*Info][]any
}

func (s *Set) add(i *Info, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[*Info][]any{}
	}
	s.m[i] = append(s.m[i], v)
}

func (s *Set) get(i *Info) []any {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]any(nil), s.m[i]...)
}

// Host gives access to an app's Set; *plugin.Registry implements it.
type Host interface{ Hooks() *Set }

// Kind of an extension point.
type Kind string

const (
	KindAction Kind = "action"
	KindFilter Kind = "filter"
	KindList   Kind = "list"
)

// Info describes a declared extension point.
type Info struct {
	Name string
	Doc  string
	Kind Kind
	Type string // payload type, for documentation
}

var (
	declMu   sync.Mutex
	declared = map[string]*Info{}
)

func declare(name, doc string, k Kind, typ string) *Info {
	declMu.Lock()
	defer declMu.Unlock()
	if _, dup := declared[name]; dup {
		panic(fmt.Sprintf("hook: %q declared twice", name))
	}
	i := &Info{Name: name, Doc: doc, Kind: k, Type: typ}
	declared[name] = i
	return i
}

// All lists every declared extension point, sorted by name.
func All() []Info {
	declMu.Lock()
	defer declMu.Unlock()
	out := make([]Info, 0, len(declared))
	for _, i := range declared {
		out = append(out, *i)
	}
	slices.SortFunc(out, func(a, b Info) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// Action notifies plugins that something happened.
type Action[T any] struct{ Info *Info }

// NewAction declares an action hook; name must be unique.
func NewAction[T any](name, doc string) *Action[T] {
	var zero T
	return &Action[T]{Info: declare(name, doc, KindAction, fmt.Sprintf("%T", zero))}
}

// On registers fn.
func (a *Action[T]) On(h Host, fn func(ctx context.Context, v T) error) {
	h.Hooks().add(a.Info, fn)
}

// Run calls all functions in order and stops at the first error, which it
// returns wrapped with the hook name. The caller decides whether that error
// aborts its operation or is only logged.
func (a *Action[T]) Run(ctx context.Context, h Host, v T) error {
	for _, f := range h.Hooks().get(a.Info) {
		if err := f.(func(context.Context, T) error)(ctx, v); err != nil {
			return fmt.Errorf("%s: %w", a.Info.Name, err)
		}
	}
	return nil
}

// Filter lets plugins change a value; each function receives the result
// of the previous one.
type Filter[T any] struct{ Info *Info }

// NewFilter declares a filter hook; name must be unique.
func NewFilter[T any](name, doc string) *Filter[T] {
	var zero T
	return &Filter[T]{Info: declare(name, doc, KindFilter, fmt.Sprintf("%T", zero))}
}

// Filter registers fn.
func (f *Filter[T]) Filter(h Host, fn func(ctx context.Context, v T) (T, error)) {
	h.Hooks().add(f.Info, fn)
}

// Apply runs all functions in order; on error it returns the last good
// value and the error.
func (f *Filter[T]) Apply(ctx context.Context, h Host, v T) (T, error) {
	for _, fn := range h.Hooks().get(f.Info) {
		next, err := fn.(func(context.Context, T) (T, error))(ctx, v)
		if err != nil {
			return v, fmt.Errorf("%s: %w", f.Info.Name, err)
		}
		v = next
	}
	return v, nil
}

// List collects contributions, e.g. field types, admin actions or widgets.
type List[T any] struct{ Info *Info }

// NewList declares a list hook; name must be unique.
func NewList[T any](name, doc string) *List[T] {
	var zero T
	return &List[T]{Info: declare(name, doc, KindList, fmt.Sprintf("%T", zero))}
}

// Add contributes v.
func (l *List[T]) Add(h Host, v T) { h.Hooks().add(l.Info, v) }

// Items returns all contributions in registration order.
func (l *List[T]) Items(h Host) []T {
	raw := h.Hooks().get(l.Info)
	out := make([]T, len(raw))
	for i, v := range raw {
		out[i] = v.(T)
	}
	return out
}
