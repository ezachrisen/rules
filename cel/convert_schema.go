package cel

// This file contains functions that convert
//   FROM an indigo.Schema
//   TO a CEL schema
//
// The resulting CEL schema is passed to the CEL compiler to validate the rule expression
// and perform type checking on it.

import (
	"fmt"

	"github.com/ezachrisen/indigo"
	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	gexpr "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// ConvertIndigoSchemaToDeclarations converts an Indigo Schema to a list of CEL "EnvOption".
// Entries in this list are types that CEL know about (i.e., the schema).
func ConvertIndigoSchemaToDeclarations(s indigo.Schema) ([]celgo.EnvOption, error) {

	// declarations are what CEL calls types in their schema
	declarations := []*gexpr.Decl{}

	// for protocol buffer types we also have to register the type separately
	// we'll collect them in types
	types := []interface{}{}

	for _, d := range s.Elements {
		typ, err := convertIndigoToExprType(d.Type)

		if err != nil {
			return nil, fmt.Errorf("converting element %s in schema %s: %v", s.Name, d.Name, err)
		}
		declarations = append(declarations, decls.NewVar(d.Name, typ))

		if v, ok := d.Type.(indigo.Proto); ok {
			types = append(types, v.Message)
		}
	}

	opts := []celgo.EnvOption{}
	opts = append(opts, celgo.Declarations(declarations...))
	opts = append(opts, celgo.Types(types...))
	if len(opts) == 0 {
		return nil, fmt.Errorf("no valid schema")
	}

	return opts, nil
}

// convertIndigoToExprType converts from an indigo type to a expr.Type,
// which is used by CEL to represent types in its schema.
func convertIndigoToExprType(t indigo.Type) (*gexpr.Type, error) {

	switch v := t.(type) {
	case indigo.String:
		return decls.String, nil
	case indigo.Int:
		return decls.Int, nil
	case indigo.Float:
		return decls.Double, nil
	case indigo.Bool:
		return decls.Bool, nil
	case indigo.Duration:
		return decls.Duration, nil
	case indigo.Timestamp:
		return decls.Timestamp, nil
	case indigo.Map:
		key, err := convertIndigoToExprType(v.KeyType)
		if err != nil {
			return nil, fmt.Errorf("setting key of %v map: %w", v.KeyType, err)
		}
		val, err := convertIndigoToExprType(v.ValueType)
		if err != nil {
			return nil, fmt.Errorf("setting value of %v map: %w", v.ValueType, err)
		}
		return decls.NewMapType(key, val), nil
	case indigo.List:
		val, err := convertIndigoToExprType(v.ValueType)
		if err != nil {
			return nil, fmt.Errorf("setting value of %v list: %w", v.ValueType, err)
		}
		return decls.NewListType(val), nil
	case indigo.Proto:
		n, err := v.ProtoFullName()
		if err != nil {
			return nil, err
		}
		return decls.NewObjectType(n), nil
	default:
		return nil, fmt.Errorf("unknown indigo type %s", t)
	}
}
