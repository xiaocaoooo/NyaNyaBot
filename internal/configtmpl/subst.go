package configtmpl

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode"
)

// Apply replaces placeholders in all JSON string values.
//
// Rules:
//  1. Global reference: ${global:name}
//  2. Env reference: ${env:NAME}
//  3. Escape: \${global:name} => literal ${global:name}
//  4. Unknown variables are kept as-is
//
// This is intended for plugin configuration before it is passed to plugins.
func Apply(config json.RawMessage, globals map[string]string) (json.RawMessage, error) {
	b := bytesTrimSpace([]byte(config))
	if len(b) == 0 {
		return json.RawMessage("{}"), nil
	}
	if b[0] != '{' {
		return nil, errors.New("config must be a JSON object")
	}

	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}

	changed := applyToValue(&v, globals)
	if !changed {
		// Keep original bytes to avoid churn.
		return json.RawMessage(b), nil
	}

	out, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func applyToValue(v *any, globals map[string]string) bool {
	switch x := (*v).(type) {
	case map[string]any:
		changed := false
		for k := range x {
			vv := x[k]
			if applyToValue(&vv, globals) {
				x[k] = vv
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for i := range x {
			vv := x[i]
			if applyToValue(&vv, globals) {
				x[i] = vv
				changed = true
			}
		}
		return changed
	case string:
		y, ch := substituteString(x, globals)
		if ch {
			*v = y
		}
		return ch
	default:
		return false
	}
}

func substituteString(s string, globals map[string]string) (string, bool) {
	// Fast path.
	if s == "" || (!strings.Contains(s, "${") && !strings.Contains(s, `\${`)) {
		return s, false
	}

	var b strings.Builder
	b.Grow(len(s))
	changed := false

	for i := 0; i < len(s); {
		// Escape: \${...} => ${...}
		if s[i] == '\\' && i+2 < len(s) && s[i+1] == '$' && s[i+2] == '{' {
			end := findClosingBrace(s, i+3)
			if end >= 0 {
				b.WriteString(s[i+1 : end+1])
				i = end + 1
				changed = true
				continue
			}
			b.WriteByte(s[i])
			i++
			continue
		}

		// Placeholder: ${global:NAME} or ${env:NAME}
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			end := findClosingBrace(s, i+2)
			if end >= 0 {
				name := s[i+2 : end]
				// ${env:XXX} reads from process environment variables.
				if strings.HasPrefix(name, "env:") {
					envKey := strings.TrimPrefix(name, "env:")
					if isValidVarName(envKey) {
						if val, ok := os.LookupEnv(envKey); ok {
							b.WriteString(val)
							changed = true
							i = end + 1
							continue
						}
					}
				} else if strings.HasPrefix(name, "global:") {
					globalKey := strings.TrimPrefix(name, "global:")
					if isValidVarName(globalKey) {
						if val, ok := globals[globalKey]; ok {
							b.WriteString(val)
							changed = true
							i = end + 1
							continue
						}
					}
				}
				// Unknown or invalid: keep original
				b.WriteString(s[i : end+1])
				i = end + 1
				continue
			}
			// not well-formed, keep '$'
			b.WriteByte(s[i])
			i++
			continue
		}

		b.WriteByte(s[i])
		i++
	}

	if !changed {
		return s, false
	}
	return b.String(), true
}

func findClosingBrace(s string, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == '}' {
			return i
		}
	}
	return -1
}

func isValidVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !(r == '_' || unicode.IsLetter(r)) {
				return false
			}
			continue
		}
		if !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func bytesTrimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j {
		s := b[i]
		if s != ' ' && s != '\n' && s != '\r' && s != '\t' {
			break
		}
		i++
	}
	for j > i {
		s := b[j-1]
		if s != ' ' && s != '\n' && s != '\r' && s != '\t' {
			break
		}
		j--
	}
	return b[i:j]
}
