package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
)

var (
	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)

// Remote addresses one service without a compile-time API shape.
type Remote struct {
	peer    *RpcPeer
	name    string
	options CallOptions
}

func (p *RpcPeer) Use(token Token, options CallOptions) *Remote {
	return &Remote{peer: p, name: token.ServiceName(), options: options}
}
func (r *Remote) Call(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	return r.peer.CallWith(ctx, r.options, r.name+"."+method, args...)
}

// Use binds each API function field to the same named remote method. Go has no generic methods or dynamic property proxy, so the typed form is a package function.
func Use[API, Event any](peer *RpcPeer, token ServiceToken[API, Event], options CallOptions) (API, error) {
	var api API
	value := reflect.ValueOf(&api).Elem()
	if value.Kind() != reflect.Struct {
		return api, fmt.Errorf("service API must be a struct of functions")
	}
	for i := range value.NumField() {
		field := value.Field(i)
		member := value.Type().Field(i).Name
		if !field.CanSet() {
			return api, fmt.Errorf("service member %s is not exported", member)
		}
		if err := checkFunction(field.Type()); err != nil {
			return api, fmt.Errorf("service member %s: %w", member, err)
		}
		method := token.Name + "." + wireMethod(member)
		field.Set(reflect.MakeFunc(field.Type(), func(args []reflect.Value) []reflect.Value {
			ctx := args[0].Interface().(context.Context)
			wire := make([]any, 0, len(args)-1)
			for _, arg := range args[1:] {
				wire = append(wire, arg.Interface())
			}
			result, err := peer.CallWith(ctx, options, method, wire...)
			return remoteResult(field.Type(), result, err)
		}))
	}
	return api, nil
}

func remoteResult(signature reflect.Type, result json.RawMessage, err error) []reflect.Value {
	out := make([]reflect.Value, signature.NumOut())
	for i := range out {
		out[i] = reflect.Zero(signature.Out(i))
	}
	if len(out) == 2 && err == nil {
		value := reflect.New(signature.Out(0))
		err = json.Unmarshal(result, value.Interface())
		if err == nil {
			out[0] = value.Elem()
		}
	}
	if err != nil {
		out[len(out)-1] = reflect.ValueOf(err)
	}
	return out
}

func serviceHandlers(implementation any) (Service, error) {
	if service, ok := implementation.(Service); ok {
		return maps.Clone(service), nil
	}
	value := reflect.ValueOf(implementation)
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return nil, fmt.Errorf("service implementation must be a Service or API struct")
	}
	service := make(Service)
	for i := range value.NumField() {
		function := value.Field(i)
		member := value.Type().Field(i).Name
		if !function.CanInterface() {
			return nil, fmt.Errorf("service member %s is not exported", member)
		}
		if err := checkFunction(function.Type()); err != nil {
			return nil, fmt.Errorf("service member %s: %w", member, err)
		}
		if function.IsNil() {
			continue
		}
		service[wireMethod(member)] = func(ctx context.Context, args []json.RawMessage) (any, error) { return invoke(function, ctx, args) }
	}
	return service, nil
}

func checkFunction(signature reflect.Type) error {
	if signature.Kind() != reflect.Func || signature.IsVariadic() || signature.NumIn() == 0 || signature.In(0) != contextType {
		return fmt.Errorf("method must be a nonvariadic function with a context argument")
	}
	if n := signature.NumOut(); (n != 1 && n != 2) || signature.Out(n-1) != errorType {
		return fmt.Errorf("method must return error or (value, error)")
	}
	return nil
}

func invoke(function reflect.Value, ctx context.Context, args []json.RawMessage) (any, error) {
	signature := function.Type()
	if len(args) != signature.NumIn()-1 {
		return nil, fmt.Errorf("expected %d arguments, received %d", signature.NumIn()-1, len(args))
	}
	values := make([]reflect.Value, signature.NumIn())
	values[0] = reflect.ValueOf(ctx)
	for i, arg := range args {
		value := reflect.New(signature.In(i + 1))
		if err := json.Unmarshal(arg, value.Interface()); err != nil {
			return nil, fmt.Errorf("argument %d: %w", i, err)
		}
		values[i+1] = value.Elem()
	}
	out := function.Call(values)
	if err, ok := out[len(out)-1].Interface().(error); ok {
		return nil, err
	}
	if len(out) == 2 {
		return out[0].Interface(), nil
	}
	return nil, nil
}

func wireMethod(name string) string { return strings.ToLower(name[:1]) + name[1:] }
