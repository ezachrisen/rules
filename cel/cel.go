package cel

import (
	"fmt" // required by CEL to construct a proto from an expression
	"strings"
	"sync"

	"github.com/ezachrisen/indigo"

	celgo "github.com/google/cel-go/cel"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Evaluator implements the indigo.ExpressionEvaluator and indigo.ExpressionCompiler interfaces.
// It uses the CEL-Go package to compile and evaluate rules.
type Evaluator struct {
	// See the [FixedSchema] option
	fixedSchema *indigo.Schema
	fixedEnv    *celgo.Env
	fixedOnce   sync.Once
}

// celProgram holds a compiled CEL Program and
// optionally an AST. The AST is used if we're collecting diagnostics
// for the engine. Indigo will attach celProgram to the rule during compilation.
type celProgram struct {
	program celgo.Program
	ast     *celgo.Ast
}

// NewEvaluator creates a new CEL Evaluator.
// The evaluator contains internal data used to facilitate CEL expression evaluation.
func NewEvaluator(opts ...CelOption) *Evaluator {
	e := &Evaluator{}
	for _, o := range opts {
		o(e)
	}
	return e
}

type CelOption func(e *Evaluator)

// FixedSchema mandates that the evaluator will use this schema for all
// compilations and evaluations. Schemas set on rules will be ignored.  CEL's
// process to create a celgo.Env from a schema is time consuming; setting the
// FixedSchema option reduces compilation time by 25% or more. The schema will
// be evaluated the first time compilation runs.
func FixedSchema(schema *indigo.Schema) CelOption {
	return func(e *Evaluator) {
		e.fixedSchema = schema
	}
}

// WithFixedEnv mandates that the evaluator will use this CEL environment.
// Doing so, we could access some features from CEL, directly, like adding
// the extension functions, or allowing the user to add their own functions.
func WithFixedEnv(env *celgo.Env) CelOption {
	return func(e *Evaluator) {
		e.fixedEnv = env
	}
}

// Compile checks a rule, prepares a compiled CELProgram, and stores the program
// in rule.Program. CELProgram contains the compiled program used to evaluate the rules,
// and if we're collecting diagnostics, CELProgram also contains the CEL AST to provide
// type and symbol information in diagnostics.
//
// Any errors in compilation are returned with a nil program
func (e *Evaluator) Compile(expr string, s indigo.Schema, resultType indigo.Type, collectDiagnostics bool, _ bool) (interface{}, error) {

	// A blank expression is ok, but it won't pass through the compilation
	if expr == "" {
		return nil, nil
	}

	prog := celProgram{}
	var err error

	e.fixedOnce.Do(func() {
		if e.fixedSchema == nil || e.fixedEnv != nil {
			return
		}
		env, errx := celEnv(*e.fixedSchema)
		if errx != nil {
			err = errx
			return
		}
		e.fixedEnv = env
	})

	if err != nil {
		return nil, fmt.Errorf("converting evaluator schema: %w", err)
	}

	var env *celgo.Env
	if e.fixedEnv == nil {
		env, err = celEnv(s)
		if err != nil {
			return nil, err
		}
	} else {
		env = e.fixedEnv
	}

	if env == nil {
		return nil, fmt.Errorf("no valid CEL environment")
	}

	// Parse the rule expression to an AST
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		// Remove some wonky formatting from CEL's error message.
		return nil, fmt.Errorf("parsing rule:\n%s", strings.ReplaceAll(fmt.Sprintf("%s", iss.Err()), "<input>:", ""))
	}

	// Type-check the parsed AST against the declarations
	c, iss := env.Check(ast)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("checking rule:\n%w", iss.Err())
	}

	if err := doTypesMatch(c.ResultType(), resultType); err != nil {
		return nil, fmt.Errorf("result type mismatch: %w", err)
	}

	if collectDiagnostics {
		prog.ast = ast
	}

	options := celgo.EvalOptions()
	if collectDiagnostics {
		options = celgo.EvalOptions(celgo.OptTrackState)
	}
	prog.program, err = env.Program(c, options)
	if err != nil {
		return nil, fmt.Errorf("generating program: %w", err)
	}

	return prog, nil
}

func celEnv(schema indigo.Schema) (*celgo.Env, error) {

	opts, err := ConvertIndigoSchemaToDeclarations(schema)
	if err != nil {
		return nil, err
	}

	env, err := celgo.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	return env, nil

}

// Evaluate a rule against the input data.
// Called by indigo.Engine.Evaluate for the rule and its children.
func (*Evaluator) Evaluate(data map[string]interface{}, expr string, _ indigo.Schema, _ interface{},
	evalData interface{}, expectedResultType indigo.Type, returnDiagnostics bool) (interface{}, *indigo.Diagnostics, error) {

	program, ok := evalData.(celProgram)

	// If the rule doesn't have a program, return a default result
	if !ok {

		// No program is ok if there's no expression to evauate, otherwise
		// it is an error
		if expr == "" {
			return true, nil, nil
		}
		return nil, nil, fmt.Errorf("missing program")
	}

	rawValue, details, err := program.program.Eval(data)

	// Do not check the error yet. Grab the diagnostics first
	var diagnostics *indigo.Diagnostics
	if returnDiagnostics {
		//		fmt.Println("collecting diagnostics")
		diagnostics, err = collectDiagnostics(program.ast, details, data)
		if err != nil {
			return nil, nil, fmt.Errorf("collecting diagnostics: %w", err)
		}
	}

	if err != nil {
		return nil, diagnostics, fmt.Errorf("evaluating rule: %w", err)
	}

	if rawValue == nil {
		return nil, diagnostics, nil
	}
	//	fmt.Println("Before returning", expr, "diagnostics = ", diagnostics)
	// The output from CEL evaluation is a ref.Val.
	// The underlying Go value is returned by .Value()
	// One type requires special handling: protocol buffers dynamically constructed
	// by CEL in the expression.
	switch rawValue.Value().(type) {
	case *dynamicpb.Message:
		// If CEL returns a protocol buffer, attempt to convert it to the
		// type of protocol buffer we expected to get.
		pb, err := convertDynamicMessageToProto(rawValue, expectedResultType)
		return pb, diagnostics, err
	default:
		return rawValue.Value(), diagnostics, err
	}
}
