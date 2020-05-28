// package cel provides an implementation of the rules/Engine interface backed by Google's cel-go rules engine
// See https://github.com/google/cel-go and https://opensource.google/projects/cel for more information
// about CEL. The rules you write must conform to the CEL spec: https://github.com/google/cel-spec.

package cel

import (
	"fmt"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/ezachrisen/rules"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common/types/pb"
	exprbp "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"google.golang.org/protobuf/runtime/protoiface"
)

type CELEngine struct {
	// Rules holds the raw rules passed by the user of the engine.
	rules map[string]rules.Rule

	// Rules are parsed, checked and stored as runnable CEL prorgrams
	programs map[string]cel.Program
}

// Initialize a new CEL Engine
func NewEngine() *CELEngine {
	engine := CELEngine{}
	engine.rules = make(map[string]rules.Rule)
	engine.programs = make(map[string]cel.Program)
	return &engine
}

// AddRule compiles the rule and adds it to the engine, ready to
// be evaluated.
// Any errors from the compilation will be returned.
func (e *CELEngine) AddRule(rules ...rules.Rule) error {
	for _, r := range rules {

		if len(strings.Trim(r.ID, " ")) == 0 {
			return fmt.Errorf("Required rule ID for rule with expression %s", r.Expr)
		}

		err := e.addRuleWithSchema(r, r.Schema)
		if err != nil {
			return err
		}
		e.rules[r.ID] = r
	}
	return nil
}

// Find a rule with the given ID
func (e *CELEngine) Rule(id string) (rules.Rule, bool) {
	r, ok := e.rules[id]
	return r, ok
}

func (e *CELEngine) PrintStructure() {
	fmt.Println("-------------------------------------------------- RULES")
	//	fmt.Printf("%v\n", e.rules)
	spew.Config.Indent = "\t"
	spew.Config.DisableCapacities = true
	spew.Config.DisablePointerAddresses = true
	spew.Config.DisablePointerMethods = true

	// spew.Dump(e.rules)
	fmt.Println("-------------------------------------------------- PROGRAMS")
	spew.Config.MaxDepth = 1
	//	spew.Dump(e.programs)
}

// Remove rule with the ID
func (e *CELEngine) RemoveRule(id string) {
	delete(e.rules, id)
	delete(e.programs, id)
}

func (e *CELEngine) RuleCount() int {
	return len(e.rules)
}

// Evaluate the rule agains the input data.
// All rules will be evaluated, descending down through child rules up to the maximum depth
func (e *CELEngine) Evaluate(data map[string]interface{}, id string, opts ...rules.Option) (*rules.Result, error) {
	o := rules.EvalOptions{
		MaxDepth:   rules.DefaultDepth,
		ReturnFail: true,
		ReturnPass: true,
	}

	rules.ApplyOptions(&o, opts...) // TODO: remove?

	rule, ok := e.rules[id]
	if !ok {
		return nil, fmt.Errorf("Rule not found")
	}

	return e.evaluate(data, rule, 0, o)
}

// Recursively evaluate the rule and its child rules.
func (e *CELEngine) evaluate(data map[string]interface{}, rule rules.Rule, n int, opt rules.EvalOptions) (*rules.Result, error) {

	if n > opt.MaxDepth {
		return nil, nil
	}

	pr := rules.Result{
		Meta:    rule.Meta,
		Action:  rule.Action,
		RuleID:  rule.ID,
		Results: make(map[string]rules.Result),
		Depth:   n,
	}

	// Apply options for this rule evaluation
	rules.ApplyOptions(&opt, rule.Opts...)

	program, found := e.programs[rule.ID]
	// If the rule has an expression, evaluate it
	if program != nil && found {
		addSelf(data, rule.Self)
		rawValue, _, error := program.Eval(data)
		if error != nil {
			return nil, fmt.Errorf("Error evaluating rule %s:%w", rule.ID, error)
		}

		pr.Value = rawValue.Value()
		if v, ok := rawValue.Value().(bool); ok {
			pr.Pass = v
		} else {
			pr.Pass = false
		}
	} else {
		// If the rule has no expression default the result to true
		// Likely this means that this rule is a "set" of child rules,
		// and the user is only interested in the result of the children.
		pr.Value = true
		pr.Pass = true
	}

	if opt.StopIfParentNegative && pr.Pass == false {
		return &pr, nil
	}

	// Evaluate child rules
	for _, c := range rule.Rules {
		res, err := e.evaluate(data, c, n+1, opt)
		if err != nil {
			return nil, err
		}
		if res != nil {
			if (!res.Pass && opt.ReturnFail) ||
				(res.Pass && opt.ReturnPass) {
				pr.Results[c.ID] = *res
			}
		}

		if opt.StopFirstPositiveChild && res.Pass == true {
			return &pr, nil
		}

		if opt.StopFirstNegativeChild && res.Pass == false {
			return &pr, nil
		}
	}
	return &pr, nil
}

// Calculate a numeric expression and return the result.
// Although a numeric expression can be calculated in any rule and the result returned via Result.Value,
// this function provides type conversion convenience when it is known that the result will be numeric.
// If the expression is not already present, it will be compiled and added, before evaluating.
// Subsequent invocations of the expression will use the compiled progrom to evaluate.
// Returns an error if the expression evaluation result is not float64.
func (e *CELEngine) Calculate(data map[string]interface{}, expr string, schema rules.Schema) (float64, error) {
	prg, found := e.programs[expr]
	if !found {
		r := rules.Rule{
			ID:     expr,
			Expr:   expr,
			Schema: schema,
		}
		err := e.AddRule(r)
		if err != nil {
			return 0.0, err
		}
		prg = e.programs[expr]
	}

	result, err := e.evaluateProgram(data, prg)
	if err != nil {
		return 0.0, err
	}

	val, ok := result.(float64)
	if !ok {
		return 0.0, fmt.Errorf("Result (%v), type %T could not be converted to float64", result, result)
	}
	return val, nil
}

// Add the self object (if provided) to the data
func addSelf(data map[string]interface{}, self interface{}) {
	if self != nil {
		data[rules.SelfKey] = self
	} else {
		delete(data, rules.SelfKey)
	}
}

// Parse, check and store the rule
func (e *CELEngine) compileRule(env *cel.Env, r rules.Rule) (cel.Program, error) {

	// Parse the rule expression to an AST
	p, iss := env.Parse(r.Expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("parsing rule %s, %w", r.ID, iss.Err())
	}

	// Type-check the parsed AST against the declarations
	c, iss := env.Check(p)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("checking rule %s, %w", r.ID, iss.Err())
	}

	// Generate an evaluable program
	prg, err := env.Program(c)
	if err != nil {
		return nil, fmt.Errorf("generating program %s, %w", r.ID, err)
	}
	return prg, nil
}

// Excecute the stored program and provide the results
func (e *CELEngine) evaluateProgram(data map[string]interface{}, prg cel.Program) (interface{}, error) {
	rawValue, _, err := prg.Eval(data)
	if err != nil {
		return nil, err
	}
	return rawValue.Value(), nil
}

func (e *CELEngine) addRuleWithSchema(r rules.Rule, s rules.Schema) error {

	var decls cel.EnvOption
	var err error
	var schemaToPassOn rules.Schema
	// If the rule has a schema, use it, otherwise use the parent rule's
	if len(r.Schema.Elements) > 0 {
		decls, err = schemaToDeclarations(r.Schema)
		if err != nil {
			return err
		}
		schemaToPassOn = r.Schema
	} else if len(s.Elements) > 0 {
		decls, err = schemaToDeclarations(s)
		if err != nil {
			return err
		}
		schemaToPassOn = s
	}

	if decls == nil {
		return fmt.Errorf("No valid schema for rule %s", r.ID)
	}

	env, err := cel.NewEnv(decls)
	if err != nil {
		return err
	}

	if r.Expr != "" {
		prg, err := e.compileRule(env, r)
		if err != nil {
			return fmt.Errorf("compiling rule %s: %w", r.ID, err)
		}
		e.programs[r.ID] = prg
	}

	for _, c := range r.Rules {
		err = e.addRuleWithSchema(c, schemaToPassOn)
		if err != nil {
			return fmt.Errorf("adding rule %s: %w", c.ID, err)
		}
	}
	return nil
}

// celType converts from a rules.Type to a CEL type
func celType(t rules.Type) (*exprbp.Type, error) {

	switch v := t.(type) {
	case rules.String:
		return decls.String, nil
	case rules.Int:
		return decls.Int, nil
	case rules.Float:
		return decls.Double, nil
	case rules.Bool:
		return decls.Bool, nil
	case rules.Duration:
		return decls.Duration, nil
	case rules.Timestamp:
		return decls.Timestamp, nil
	case rules.Map:
		key, err := celType(v.KeyType)
		if err != nil {
			return nil, fmt.Errorf("Setting key of %v map: %w", v.KeyType, err)
		}
		val, err := celType(v.ValueType)
		if err != nil {
			return nil, fmt.Errorf("Setting value of %v map: %w", v.ValueType, err)
		}
		return decls.NewMapType(key, val), nil
	case rules.List:
		val, err := celType(v.ValueType)
		if err != nil {
			return nil, fmt.Errorf("Setting value of %v list: %w", v.ValueType, err)
		}
		return decls.NewListType(val), nil
	case rules.Proto:
		protoMessage, ok := v.Message.(protoiface.MessageV1)
		if !ok {
			return nil, fmt.Errorf("Casting to proto message %v", v.Protoname)
		}
		_, err := pb.DefaultDb.RegisterMessage(protoMessage)
		if err != nil {
			return nil, fmt.Errorf("registering proto message %v: %w", v.Protoname, err)
		}
		return decls.NewObjectType(v.Protoname), nil
	}
	return decls.Any, nil

}

// schemaToDeclarations converts from a rules/Schema to a set of CEL declarations that
// are passed to the CEL engine
func schemaToDeclarations(s rules.Schema) (cel.EnvOption, error) {
	items := []*exprbp.Decl{}

	for _, d := range s.Elements {
		typ, err := celType(d.Type)
		if err != nil {
			return nil, err
		}
		items = append(items, decls.NewIdent(d.Name, typ, nil))
	}
	return cel.Declarations(items...), nil
}
