package val

import (
	"fmt"
	"reflect"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/interpreter"
)

type Validator struct {
	rules map[reflect.Type]*structRules
}

func CreateValidator(structs ...any) (*Validator, error) {
	env, err := cel.NewEnv(
		cel.Variable("self", cel.DynType),
	)
	if err != nil {
		return nil, err
	}

	v := &Validator{
		rules: make(map[reflect.Type]*structRules, len(structs)),
	}

	for _, item := range structs {
		typ := reflect.TypeOf(item)

		if typ == nil {
			return nil, fmt.Errorf("nil struct type")
		}

		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}

		if typ.Kind() != reflect.Struct {
			return nil, fmt.Errorf("expected struct, got %s", typ)
		}

		if _, exists := v.rules[typ]; exists {
			continue
		}

		sr := &structRules{
			fields: make([]fieldRule, 0, typ.NumField()),
		}

		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)

			if field.PkgPath != "" {
				continue
			}

			expression := field.Tag.Get("val")
			if expression == "" {
				continue
			}

			ast, issues := env.Compile(expression)
			if issues.Err() != nil {
				return nil, fmt.Errorf(
					"%s.%s: %w",
					typ.Name(),
					field.Name,
					issues.Err(),
				)
			}

			program, err := env.Program(ast, cel.EvalOptions(cel.OptOptimize))
			if err != nil {
				return nil, fmt.Errorf(
					"%s.%s: %w",
					typ.Name(),
					field.Name,
					err,
				)
			}

			sr.fields = append(sr.fields, fieldRule{
				index:   i,
				name:    field.Name,
				message: field.Tag.Get("valerr"),
				program: program,
			})
		}

		v.rules[typ] = sr
	}

	return v, nil
}

func (v *Validator) Validate(input any) []error {
	value := reflect.ValueOf(input)

	if !value.IsValid() {
		return []error{fmt.Errorf("cannot validate nil")}
	}

	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return []error{fmt.Errorf("cannot validate nil pointer")}
		}
		value = value.Elem()
	}

	if value.Kind() != reflect.Struct {
		return []error{
			fmt.Errorf("expected struct, got %s", value.Kind()),
		}
	}

	rules := v.rules[value.Type()]
	if rules == nil {
		return []error{
			fmt.Errorf("no rules registered for %s", value.Type()),
		}
	}

	var errs []error
	activation := activation{}

	for i := range rules.fields {
		rule := &rules.fields[i]

		activation.value = value.Field(rule.index).Interface()

		result, _, err := rule.program.Eval(&activation)
		if err != nil {
			errs = append(errs, fmt.Errorf(
				"field %s: %w",
				rule.name,
				err,
			))
			continue
		}

		valid, ok := result.(types.Bool)
		if !ok || !bool(valid) {
			msg := rule.message
			if msg == "" {
				msg = "validation failed"
			}
			errs = append(errs, &fieldError{Field: rule.name, Message: msg})
		}
	}

	return errs
}

type fieldError struct {
	Field   string
	Message string
}

func (e *fieldError) Error() string {
	return e.Field + ": " + e.Message
}

type fieldRule struct {
	index   int
	name    string
	message string
	program cel.Program
}

type structRules struct {
	fields []fieldRule
}

type activation struct {
	value any
}

func (a *activation) ResolveName(name string) (any, bool) {
	if name != "self" {
		return nil, false
	}
	return a.value, true
}

func (a *activation) Parent() interpreter.Activation {
	return nil
}
