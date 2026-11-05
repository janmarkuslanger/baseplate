package content

import "testing"

type ref struct{ id int64 }

func (r ref) RefID() int64 { return r.id }

func TestRefID(t *testing.T) {
	cases := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int64(7), 7, true}, {7, 7, true}, {7.0, 7, true}, {"7", 7, true}, {ref{7}, 7, true},
		{nil, 0, false}, {"", 0, false}, {"x", 0, false}, {int64(0), 0, false}, {-3.0, 0, false},
		{[]any{int64(1)}, 0, false}, {true, 0, false},
	}
	for _, c := range cases {
		got, ok := RefID(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("RefID(%#v) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
