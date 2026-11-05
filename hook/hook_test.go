package hook

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type host struct{ s Set }

func (h *host) Hooks() *Set { return &h.s }

type msg struct{ Text string }

var (
	testAction = NewAction[*msg]("test.action", "test")
	testFilter = NewFilter[*msg]("test.filter", "test")
	testList   = NewList[string]("test.list", "test")
)

func TestAction(t *testing.T) {
	h := &host{}
	var order []string
	testAction.On(h, func(_ context.Context, m *msg) error { order = append(order, "a:"+m.Text); return nil })
	testAction.On(h, func(context.Context, *msg) error { return errors.New("boom") })
	testAction.On(h, func(context.Context, *msg) error { order = append(order, "never"); return nil })
	err := testAction.Run(context.Background(), h, &msg{"x"})
	if err == nil || !strings.Contains(err.Error(), "test.action: boom") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(order, ",") != "a:x" {
		t.Errorf("order = %v", order)
	}
	if err := testAction.Run(context.Background(), &host{}, &msg{}); err != nil {
		t.Errorf("other app must not see registrations: %v", err)
	}
}

func TestFilterAndList(t *testing.T) {
	h := &host{}
	testFilter.Filter(h, func(_ context.Context, m *msg) (*msg, error) { return &msg{m.Text + "1"}, nil })
	testFilter.Filter(h, func(_ context.Context, m *msg) (*msg, error) { return &msg{m.Text + "2"}, nil })
	out, err := testFilter.Apply(context.Background(), h, &msg{"x"})
	if err != nil || out.Text != "x12" {
		t.Fatalf("filter: %v %v", out, err)
	}
	testList.Add(h, "a")
	testList.Add(h, "b")
	if got := testList.Items(h); len(got) != 2 || got[0] != "a" {
		t.Errorf("list: %v", got)
	}
	names := map[string]bool{}
	for _, i := range All() {
		names[i.Name] = true
	}
	if !names["test.action"] || !names["test.filter"] || !names["test.list"] {
		t.Errorf("All: %v", All())
	}
}

func TestDuplicateDeclarationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want panic")
		}
	}()
	NewAction[int]("test.action", "dup")
}
