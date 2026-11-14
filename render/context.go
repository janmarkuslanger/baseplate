package render

import (
	"context"
	"reflect"
)

var contextType = reflect.TypeFor[context.Context]()

// bindContext returns fn without its first parameter, called with ctx, if
// that parameter is a context.Context; otherwise fn unchanged. Templates
// cannot pass a context, so plugin functions that load data get the one of
// the build or preview that renders them this way.
func bindContext(ctx context.Context, fn any) any {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.Type().NumIn() == 0 || v.Type().In(0) != contextType {
		return fn
	}
	t := v.Type()
	in := make([]reflect.Type, t.NumIn()-1)
	for i := range in {
		in[i] = t.In(i + 1)
	}
	out := make([]reflect.Type, t.NumOut())
	for i := range out {
		out[i] = t.Out(i)
	}
	c := reflect.ValueOf(&ctx).Elem() // keeps the interface type for Call
	return reflect.MakeFunc(reflect.FuncOf(in, out, t.IsVariadic()), func(args []reflect.Value) []reflect.Value {
		args = append([]reflect.Value{c}, args...)
		if t.IsVariadic() {
			return v.CallSlice(args)
		}
		return v.Call(args)
	}).Interface()
}
