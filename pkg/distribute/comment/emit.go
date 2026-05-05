package comment

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/printer"
)

// Emit writes d to w as a sequence of lines, each prepended with prefix.
// Output ends with exactly one newline. To round-trip a recognized Block
// verbatim, pass b.Indent as prefix; to introduce a new commented-out
// block, pass the convention of choice (typically "; ").
//
// The directive is rendered through the standard printer with default
// formatting options. Errors from w propagate.
func Emit(w io.Writer, d ast.Directive, prefix string) error {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, d); err != nil {
		return err
	}
	body := strings.TrimSuffix(buf.String(), "\n")
	for _, line := range strings.Split(body, "\n") {
		if _, err := fmt.Fprintf(w, "%s%s\n", prefix, line); err != nil {
			return err
		}
	}
	return nil
}
