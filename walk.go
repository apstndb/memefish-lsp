package main

import (
	"reflect"
	"slices"

	"github.com/cloudspannerecosystem/memefish/ast"
)

func Walk(node ast.Node, f func(path []string, node ast.Node) error) error {
	return walk(node, nil, f)
}

func walk(node ast.Node, path []string, f func(path []string, node ast.Node) error) error {
	typ := reflect.ValueOf(node)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldPath := slices.Concat(path, []string{field.Type().Name()})
		if field.Type().Implements(reflect.TypeFor[ast.Node]()) {
			if err := f(fieldPath, field.Interface().(ast.Node)); err != nil {
				return err
			}

			err := walk(node, fieldPath, f)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
