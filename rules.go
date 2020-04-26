package rules

import (
	"fmt"
)

// --------------------------------------------------
// Rules Engine

// The Engine interface represents a rules engine capable of evaluating rules.
// against a specific rule set.
type Engine interface {
	// Add the rule set to the engine
	// Will produce an error if the rule set already exists
	// Use AddOrReplaceRuleSet in that case.
	AddRuleSet(RuleSet) error
	AddOrReplaceRuleSet(RuleSet) error

	// Return the rule set if found
	RuleSet(ruleSetID string) (RuleSet, bool)

	// Evaluate a single rule agains the the data
	EvaluateRule(data map[string]interface{}, ruleSetID string, ruleID string) (*Result, error)

	// Evaluate all rules in a rule set against the data
	EvaluateAll(data map[string]interface{}, ruleSetID string) ([]Result, error)

	// Evaluate all rules in a rule set, but stop at the first true rule
	EvaluateUntilTrue(data map[string]interface{}, ruleSetID string) (Result, error)
}

// Result of evaluating a rule. A slice of these will be returned after evaluating a rule set.
// See the documentation for the Evaluate* methods for information on the
// result set.
type Result struct {
	RuleSetID    string
	RuleID       string
	Pass         bool // Whether the expression was satisfied by the input data
	Float64Value float64
	Int64Value   int64
	StringValue  string
	ResultType   Type
}

// These functions are intended to be called by implementors of the Engine interface.
// Engines are free to create their own implementations.
// Evaluate all rules in a rule set and return the true/false results of each rule
//
// Evaluation stops if an error happens, and partial results are returned.
func EvaluateAll(e Engine, data map[string]interface{}, ruleSetID string) ([]Result, error) {

	ruleSet, found := e.RuleSet(ruleSetID)
	if !found {
		return nil, fmt.Errorf("Ruleset %v not found", ruleSet)
	}

	results := make([]Result, 0, len(ruleSet.Rules))

	for ruleID := range ruleSet.Rules {
		result, err := e.EvaluateRule(data, ruleSetID, ruleID)
		if err != nil {
			return results, fmt.Errorf("Error evaluating rule: %v", err)
		}
		results = append(results, *result)
	}
	return results, nil
}

// Evaluate rules in the rule set, but stop as soon as a true rule is found. The true rule is returned.
// If no true rules are found, the result is nil.
func EvaluateUntilTrue(e Engine, data map[string]interface{}, ruleSetID string) (Result, error) {
	ruleSet, found := e.RuleSet(ruleSetID)
	if !found {
		return Result{}, fmt.Errorf("Ruleset %v not found", ruleSet)
	}

	for ruleID := range ruleSet.Rules {
		result, err := e.EvaluateRule(data, ruleSetID, ruleID)
		if err != nil {
			return Result{}, fmt.Errorf("Error evaluating rule: %v", err)
		}
		if result.Pass {
			return *result, nil
		}
	}
	return Result{}, nil
}

// --------------------------------------------------
// Rules

// The Rule interface provides an expression that follows the
// Common Expression Language specification (see
// https://pkg.go.dev/github.com/google/cel-go/cel for documentation)

type Rule interface {
	Expression() string
}

// Simple implementation of the Rule interface
type SimpleRule struct {
	Expr string
}

func (s SimpleRule) Expression() string {
	return s.Expr
}

// RuleSet contains a group of rules that will be evaluated together to produce results.
type RuleSet struct {
	ID         string
	Rules      map[string]Rule // The rules to evaluate. The map key is known as the "rule id"
	Schema     Schema          // The data schema that all rules and data must adhere to
	OutputType Type            // The type of the result value: bool, float, string, etc.
}

// --------------------------------------------------
// Schema

// Schema defines the keys (variable names) and their data types used in a
// rule expression. The same keys and types must be supplied in the data map
// when rules are evaluated.
type Schema struct {
	ID       string
	Elements []DataElement
}

// DataElement defines a named variable in a schema
type DataElement struct {
	ID          string
	Name        string // Short, user-friendly name of the variable
	Key         string // Name of the key used in the data and the rule expressions
	Type        Type
	Description string
}

// --------------------------------------------------
// Data Types in a Schema

// Type of data element represented in a schema
type Type interface {
	TypeName()
}

type String struct{}
type Int struct{}
type Float struct{}
type Any struct{}
type Bool struct{}
type Duration struct{}
type Timestamp struct{}

// List is an array of items
type List struct {
	ValueType Type // The type of value stored in the list
}

// Map is a map of items. Maps can be nested.
type Map struct {
	KeyType   Type
	ValueType Type
}

func (t Int) TypeName()       {}
func (t Bool) TypeName()      {}
func (t String) TypeName()    {}
func (t List) TypeName()      {}
func (t Map) TypeName()       {}
func (t Any) TypeName()       {}
func (t Duration) TypeName()  {}
func (t Timestamp) TypeName() {}
func (t Float) TypeName()     {}
