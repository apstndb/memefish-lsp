package memewalk

import (
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"

	"github.com/cloudspannerecosystem/memefish/ast"
)

func Walk(node ast.Node, f func(path []string, node ast.Node) error) error {
	return walk(node, []string{"$"}, f)
}

func fields(val reflect.Value) iter.Seq2[reflect.StructField, reflect.Value] {
	return func(yield func(reflect.StructField, reflect.Value) bool) {
		for i := range val.NumField() {
			if !yield(val.Type().Field(i), val.Field(i)) {
				return
			}
		}
	}
}

func WalkSlice[T ast.Node](nodes []T, f func(path []string, node ast.Node) error) error {
	var errs []error
	for i, node := range nodes {
		err := walk(node, []string{fmt.Sprintf("$[%d]", i)}, f)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func walk(node ast.Node, path []string, f func(path []string, node ast.Node) error) error {
	if err := f(path, node); err != nil {
		return err
	}

	val := reflect.ValueOf(node)
	for val.Kind() != reflect.Struct {
		switch val.Kind() {
		case reflect.Ptr, reflect.Interface:
			val = val.Elem()
		default:
			return nil
		}
	}

	for field, val := range fields(val) {

		switch {
		case val.Kind() == reflect.Slice && val.Type().Elem().Implements(reflect.TypeFor[ast.Node]()):
			for i, val := range val.Seq2() {
				path := slices.Concat(path, []string{"." + field.Name + "[" + fmt.Sprint(i) + "]"})

				if err := walk(safeCast[ast.Node](val), path, f); err != nil {
					return err
				}
			}
		case val.Type().Implements(reflect.TypeFor[ast.Node]()):
			path := slices.Concat(path, []string{"." + field.Name})
			if err := walk(safeCast[ast.Node](val), path, f); err != nil {
				return err
			}
		}
	}

	return nil
}

// safeCast returns argument value as parameter type.
// It is used to avoid typed-nil.
func safeCast[T comparable](val reflect.Value) T {
	var zero T
	if val.IsNil() {
		return zero
	}

	v, ok := val.Interface().(T)
	if !ok {
		return zero
	}
	return v
}
