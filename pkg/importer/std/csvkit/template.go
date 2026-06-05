package csvkit

import (
	"strings"
	"text/template"
)

// narrationFuncs is the fixed, side-effect-free function set available to a
// NarrationTemplate. Templates come from the operator's own configuration,
// so the restriction is for usability, not a trust boundary.
var narrationFuncs = template.FuncMap{
	"trim":  strings.TrimSpace,
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"default": func(def, v string) string {
		if strings.TrimSpace(v) == "" {
			return def
		}
		return v
	},
}

// NarrationTemplate is a compiled, restricted text/template that renders a
// narration from a row's named columns. Columns are referenced as template
// fields (e.g. {{.Memo}}); only the columns present in the data passed to
// Render are valid, and an unknown reference is a render error rather than
// a silent blank. The available functions are trim, upper, lower, and
// default.
type NarrationTemplate struct {
	t *template.Template
}

// CompileNarration parses src into a NarrationTemplate, returning an error
// for a malformed template so configuration problems surface at construction
// time rather than per row.
func CompileNarration(src string) (*NarrationTemplate, error) {
	t, err := template.New("narration").Option("missingkey=error").Funcs(narrationFuncs).Parse(src)
	if err != nil {
		return nil, err
	}
	return &NarrationTemplate{t: t}, nil
}

// Render executes the template against the row's columns, keyed by column
// name. A reference to a key absent from data is reported as an error.
func (nt *NarrationTemplate) Render(data map[string]string) (string, error) {
	var b strings.Builder
	if err := nt.t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
