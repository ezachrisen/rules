package indigo

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Schema defines the variable names and their data types used in a
// rule expression. The same keys and types must be supplied in the data map
// when rules are evaluated.
type Schema struct {
	// Identifier for the schema. Useful for the hosting application; not used by Indigo internally.
	ID string `json:"id,omitempty"`
	// User-friendly name for the schema
	Name string `json:"name,omitempty"`
	// A user-friendly description of the schema
	Description string `json:"description,omitempty"`
	// User-defined value
	Meta interface{} `json:"-"`
	// List of data elements supported by this schema
	Elements []DataElement `json:"elements,omitempty"`
}

// String returns a human-readable representation of the schema
func (s *Schema) String() string {
	x := strings.Builder{}
	x.WriteString(s.ID)
	if s.Name != "" {
		x.WriteString("  '" + s.Name + "'")
	}
	x.WriteString("\n")
	for _, e := range s.Elements {
		x.WriteString(e.String())
		x.WriteString("\n")
	}

	return x.String()
}

// DataElement defines a named variable in a schema
type DataElement struct {
	// Short, user-friendly name of the variable. This is the name
	// that will be used in rules to refer to data passed in.
	//
	// RESERVED NAMES:
	//   selfKey (see const)
	Name string `json:"name"`

	// One of the Type interface defined.
	Type Type `json:"type"`

	// Optional description of the type.
	Description string `json:"description"`
}

// String returns a human-readable representation of the element
func (e *DataElement) String() string {
	return fmt.Sprintf("  %s (%s)", e.Name, e.Type)
}

// Type defines a type in the Indigo type system.
// These types are used to define schemas and define required
// evaluation results.
// Not all implementations of Evaluator support all types.
type Type interface {
	// Implements the stringer interface
	String() string
}

// String defines an Indigo string type.
type String struct{}

// Int defines an Indigo int type. The exact "Int" implementation and size
// depends on the evaluator used.
type Int struct{}

// Float defines an Indigo float type. The implementation of the float (size, precision)
// depends on the evaluator used.
type Float struct{}

// Any defines an Indigo type for an "undefined" or unspecified type.
// Any can be used only as a return type.
type Any struct{}

// Bool defines an Indigo type for true/false.
type Bool struct{}

// Duration defines an Indigo type for the time.Duration type.
type Duration struct{}

// Timestamp defines an Indigo type for the time.Time type.
type Timestamp struct{}

// Proto defines an Indigo type for a protobuf type.
type Proto struct {
	Message proto.Message // an instance of the proto message
}

// ProtoFullName uses protocol buffer reflection to obtain the full name of the
// proto type.
func (p *Proto) ProtoFullName() (string, error) {
	if p == nil || p.Message == nil {
		return "", fmt.Errorf("indigo.Proto.Message is nil")
	}

	pr := p.Message.ProtoReflect()
	if pr == nil {
		return "", fmt.Errorf("indigo.Proto.Message has nil proto reflect")
	}

	desc := pr.Descriptor()
	if desc == nil {
		return "", fmt.Errorf("indigo.Proto.Message is missing Descriptor")
	}

	return string(desc.FullName()), nil

}

// List defines an Indigo type representing a slice of values
type List struct {
	ValueType Type // the type of element stored in the list
}

// Map defines an Indigo type representing a map of keys and values.
type Map struct {
	KeyType   Type // the type of the map key
	ValueType Type // the type of the value stored in the map
}

// String Methods
func (Int) String() string       { return "int" }
func (Bool) String() string      { return "bool" }
func (String) String() string    { return "string" }
func (Any) String() string       { return "any" }
func (Duration) String() string  { return "duration" }
func (Timestamp) String() string { return "timestamp" }
func (Float) String() string     { return "float" }
func (p Proto) String() string {
	s, err := p.ProtoFullName()
	if err != nil {
		return fmt.Sprintf("proto(missing name: %v)", err)
	}
	return "proto(" + s + ")"

}
func (t List) String() string { return fmt.Sprintf("[]%v", t.ValueType) }
func (t Map) String() string  { return fmt.Sprintf("map[%s]%s", t.KeyType, t.ValueType) }

// ParseType parses a string that represents an Indigo type and returns the type.
// The primitive types are their lower-case names (string, int, duration, etc.)
// Maps and lists look like Go maps and slices: map[string]float and []string.
// Proto types look like this: proto(protoname)
// Before parsing types, protocol buffer types must be available in the global
// protocol buffer registry, either by importing at compile time or registering them
// separately from a descriptor file at run time. ParseType returns an error if a
// protocol buffer type is missing.
func ParseType(t string) (Type, error) {

	if strings.Contains(t, "map") {
		return parseMap(t)
	}

	if strings.Contains(t, "[]") {
		return parseList(t)
	}

	if strings.Contains(t, "proto(") {
		return parseProto(t)
	}

	switch t {
	case "string":
		return String{}, nil
	case "int":
		return Int{}, nil
	case "float":
		return Float{}, nil
	case "bool":
		return Bool{}, nil
	case "duration":
		return Duration{}, nil
	case "timestamp":
		return Timestamp{}, nil
	case "any":
		return Any{}, nil
	default:
		return Any{}, fmt.Errorf("unrecognized type: %s", t)
	}
}

// parseMap parses a string and returns an Indigo map type.
// The string must in the format map[<keytype]<valuetype>.
// Example: map[string]int
func parseMap(t string) (Type, error) {

	var keyTypeName string
	var valueTypeName string

	t = strings.ReplaceAll(t, "[", " ")
	t = strings.ReplaceAll(t, "]", " ")

	n, err := fmt.Sscanf(t, "map %s %s", &keyTypeName, &valueTypeName)
	if err != nil {
		return Any{}, err
	}

	if n < 2 {
		return Any{}, fmt.Errorf("wanted 2 items parsed, got %d", n)
	}

	keyType, err := ParseType(keyTypeName)
	if err != nil {
		return Any{}, err
	}

	valueType, err := ParseType(valueTypeName)
	if err != nil {
		return Any{}, err
	}

	return Map{
		KeyType:   keyType,
		ValueType: valueType,
	}, nil
}

// parseList parses a string and returns an Indigo list type.
// The string must be in the format []<valuetype>
// Example: []string
func parseList(t string) (Type, error) {
	var valueTypeName string
	_, err := fmt.Sscanf(t, "[]%s", &valueTypeName)
	if err != nil {
		return Any{}, err
	}
	valueType, err := ParseType(valueTypeName)
	if err != nil {
		return Any{}, err
	}

	return List{
		ValueType: valueType,
	}, nil
}

// parseProto parses a string and returns an Indigo proto type.
// The message type must be registered in the global protocol buffer registry.
// Example: proto(school.Student)
func parseProto(t string) (Type, error) {
	startParen := strings.Index(t, "(")
	endParen := strings.Index(t, ")")

	if startParen == -1 || endParen == -1 || startParen > endParen || endParen > len(t) || endParen-startParen == 1 {
		return Any{}, fmt.Errorf("bad proto specification")
	}

	name := t[startParen+1 : endParen]
	p, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(name))
	if err != nil {
		return Any{}, err
	}
	return Proto{p.New().Interface()}, nil
}
