package val

import (
	"regexp"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/interpreter"
)

// fastMatches only speeds up simple regexes like ^[a-zA-Z0-9]+$ or ^[a-z]*$
// any other pattern still runs through the regexp package.
func fastMatches(i interpreter.InterpretableV2) (interpreter.InterpretableV2, error) {
	call, ok := i.(interpreter.InterpretableCall)
	if !ok || call.Function() != "matches" || len(call.Args()) != 2 {
		return i, nil
	}
	c, ok := call.Args()[1].(interpreter.InterpretableConst)
	if !ok {
		return i, nil
	}
	pattern, ok := c.Value().(types.String)
	if !ok {
		return i, nil
	}

	re, err := regexp.Compile(string(pattern))
	if err != nil {
		return nil, err
	}

	m := &matchNode{id: call.ID(), input: call.Args()[0], re: re}
	m.table, m.allowEmpty, m.simple = parseSimpleClass(string(pattern))
	return m, nil
}

func parseSimpleClass(p string) (table [256]bool, allowEmpty, ok bool) {
	n := len(p)
	if n < 6 || p[0] != '^' || p[1] != '[' || p[n-1] != '$' || p[n-3] != ']' {
		return table, false, false
	}
	switch p[n-2] {
	case '+':
	case '*':
		allowEmpty = true
	default:
		return table, false, false
	}

	class := p[2 : n-3]
	if class == "" || class[0] == '^' {
		return table, false, false
	}
	for i := 0; i < len(class); i++ {
		c := class[i]
		if c < 0x20 || c > 0x7e || c == '\\' || c == '[' || c == ']' {
			return table, false, false
		}
		if i+2 < len(class) && class[i+1] == '-' {
			hi := class[i+2]
			if hi < c || hi > 0x7e || hi == '\\' || hi == '[' || hi == ']' {
				return table, false, false
			}
			for b := c; ; b++ {
				table[b] = true
				if b == hi {
					break
				}
			}
			i += 2
			continue
		}
		table[c] = true
	}
	return table, allowEmpty, true
}

type matchNode struct {
	id    int64
	input interpreter.InterpretableV2
	re    *regexp.Regexp

	simple     bool
	allowEmpty bool
	table      [256]bool
}

func (m *matchNode) ID() int64 { return m.id }

func (m *matchNode) Eval(act interpreter.Activation) ref.Val {
	return interpreter.EvalActivation(act, m.Exec)
}

func (m *matchNode) Exec(frame *interpreter.ExecutionFrame) ref.Val {
	v := m.input.Exec(frame)
	s, ok := v.(types.String)
	if !ok {
		if types.IsUnknownOrError(v) {
			return v
		}
		return types.NoSuchOverloadErr()
	}

	if !m.simple {
		return types.Bool(m.re.MatchString(string(s)))
	}
	if len(s) == 0 {
		return types.Bool(m.allowEmpty)
	}
	for i := 0; i < len(s); i++ {
		if !m.table[s[i]] {
			return types.False
		}
	}
	return types.True
}
