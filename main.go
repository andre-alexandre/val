package val

import (
	"fmt"
	"reflect"
	"sync"
	"unsafe"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/interpreter"
)

var activationPool = sync.Pool{
	New: func() any {
		return new(activation)
	},
}

type Validator struct {
	rules map[reflect.Type]*structRules
}

func CreateValidator(structs ...any) (*Validator, error) {
	envs := make(map[*cel.Type]*cel.Env)
	envFor := func(t *cel.Type) (*cel.Env, error) {
		if env, ok := envs[t]; ok {
			return env, nil
		}
		env, err := cel.NewEnv(
			cel.Variable("self", t),
			cel.CrossTypeNumericComparisons(true),
		)
		if err != nil {
			return nil, err
		}
		envs[t] = env
		return env, nil
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

		for field := range typ.Fields() {
			if field.PkgPath != "" {
				continue
			}

			expression := field.Tag.Get("val")
			if expression == "" {
				continue
			}

			celType, conv := converterFor(field.Type)

			env, err := envFor(celType)
			if err != nil {
				return nil, err
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

			program, err := env.Program(ast,
				cel.CustomDecoratorV2(fastMatches),
				cel.EvalOptions(cel.OptOptimize),
			)
			if err != nil {
				return nil, fmt.Errorf(
					"%s.%s: %w",
					typ.Name(),
					field.Name,
					err,
				)
			}

			sr.fields = append(sr.fields, fieldRule{
				offset:  field.Offset,
				name:    field.Name,
				message: field.Tag.Get("valerr"),
				program: program,
				conv:    conv,
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

	var base unsafe.Pointer
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return []error{fmt.Errorf("cannot validate nil pointer")}
		}
		base = value.UnsafePointer()
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

	if base == nil {
		cp := reflect.New(value.Type())
		cp.Elem().Set(value)
		base = cp.UnsafePointer()
	}

	var errs []error
	act := activationPool.Get().(*activation)

	frame, err := interpreter.NewExecutionFrame(act)
	if err != nil {
		activationPool.Put(act)
		return []error{err}
	}
	defer func() {
		frame.Close()
		act.value = nil
		activationPool.Put(act)
	}()

	for i := range rules.fields {
		rule := &rules.fields[i]

		act.value = rule.conv(unsafe.Add(base, rule.offset))

		result, _, err := rule.program.Eval(frame)
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

func converterFor(t reflect.Type) (*cel.Type, func(unsafe.Pointer) ref.Val) {
	switch t.Kind() {
	case reflect.Bool:
		return cel.BoolType, func(p unsafe.Pointer) ref.Val { return types.Bool(*(*bool)(p)) }
	case reflect.Int:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*int)(p)) }
	case reflect.Int8:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*int8)(p)) }
	case reflect.Int16:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*int16)(p)) }
	case reflect.Int32:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*int32)(p)) }
	case reflect.Int64:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*int64)(p)) }
	case reflect.Uint8:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*uint8)(p)) }
	case reflect.Uint16:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*uint16)(p)) }
	case reflect.Uint32:
		return cel.IntType, func(p unsafe.Pointer) ref.Val { return types.Int(*(*uint32)(p)) }
	case reflect.Uint:
		return cel.UintType, func(p unsafe.Pointer) ref.Val { return types.Uint(*(*uint)(p)) }
	case reflect.Uint64:
		return cel.UintType, func(p unsafe.Pointer) ref.Val { return types.Uint(*(*uint64)(p)) }
	case reflect.Float32:
		return cel.DoubleType, func(p unsafe.Pointer) ref.Val { return types.Double(*(*float32)(p)) }
	case reflect.Float64:
		return cel.DoubleType, func(p unsafe.Pointer) ref.Val { return types.Double(*(*float64)(p)) }
	case reflect.String:
		return cel.StringType, func(p unsafe.Pointer) ref.Val { return types.String(*(*string)(p)) }
	default:
		return cel.DynType, func(p unsafe.Pointer) ref.Val {
			return types.DefaultTypeAdapter.NativeToValue(reflect.NewAt(t, p).Elem().Interface())
		}
	}
}

type fieldError struct {
	Field   string
	Message string
}

func (e *fieldError) Error() string {
	return e.Field + ": " + e.Message
}

type fieldRule struct {
	offset  uintptr
	name    string
	message string
	program cel.Program
	conv    func(unsafe.Pointer) ref.Val
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
