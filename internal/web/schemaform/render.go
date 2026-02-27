package schemaform

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
)

// RenderHTML renders form fields HTML for the given schema and values.
// Field names are the JSON property names.
func RenderHTML(form Form, values map[string]any) string {
	// Stable field order (already sorted in Parse, but keep safe)
	fields := append([]Field(nil), form.Fields...)
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })

	var b strings.Builder
	for _, f := range fields {
		label := f.Title
		if label == "" {
			label = f.Name
		}
		desc := f.Description
		reqMark := ""
		if f.Required {
			reqMark = " <span class=\"req\">*</span>"
		}

		b.WriteString("<div class=\"field\">")
		b.WriteString("<label for=\"" + html.EscapeString(f.Name) + "\">" + html.EscapeString(label) + reqMark + "</label>")

		if desc != "" {
			b.WriteString("<div class=\"help\">" + html.EscapeString(desc) + "</div>")
		}

		val := values[f.Name]
		switch f.Type {
		case FieldString:
			b.WriteString("<input id=\"" + html.EscapeString(f.Name) + "\" name=\"" + html.EscapeString(f.Name) + "\" value=\"" + html.EscapeString(toString(val)) + "\">")
		case FieldNumber:
			b.WriteString("<input id=\"" + html.EscapeString(f.Name) + "\" name=\"" + html.EscapeString(f.Name) + "\" type=\"number\" step=\"any\" value=\"" + html.EscapeString(toString(val)) + "\">")
		case FieldInteger:
			b.WriteString("<input id=\"" + html.EscapeString(f.Name) + "\" name=\"" + html.EscapeString(f.Name) + "\" type=\"number\" step=\"1\" value=\"" + html.EscapeString(toString(val)) + "\">")
		case FieldBoolean:
			checked := ""
			if vb, ok := val.(bool); ok && vb {
				checked = " checked"
			}
			b.WriteString("<label class=\"check\"><input id=\"" + html.EscapeString(f.Name) + "\" name=\"" + html.EscapeString(f.Name) + "\" type=\"checkbox\"" + checked + "> <span>启用</span></label>")
		default:
			// ignore
		}

		b.WriteString("</div>")
	}

	return b.String()
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	case int64:
		return fmt.Sprintf("%d", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
