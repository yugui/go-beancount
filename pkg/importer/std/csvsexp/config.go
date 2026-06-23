package csvsexp

import (
	"fmt"
	"strings"

	"github.com/yugui/go-beancount/pkg/importer"
)

// config is the on-disk shape of a csvsexp instance: a single field holding the
// whole S-expression program.
type config struct {
	Program string `toml:"program"`
}

// newImporter is the factory registered under kind "csv-sexp". It decodes the
// program string and compiles it into a [*csvbase.Driver] bound to name,
// returning an error when the program is missing or fails to compile.
func newImporter(name string, decode func(dest any) error) (importer.Importer, error) {
	if decode == nil {
		return nil, fmt.Errorf("csvsexp: configure: nil decoder")
	}
	var cfg config
	if err := decode(&cfg); err != nil {
		return nil, fmt.Errorf("csvsexp: configure: %w", err)
	}
	if strings.TrimSpace(cfg.Program) == "" {
		return nil, fmt.Errorf("csvsexp: configure: program is required")
	}
	drv, err := compileProgram(name, cfg.Program)
	if err != nil {
		return nil, fmt.Errorf("csvsexp: configure: %w", err)
	}
	return drv, nil
}
