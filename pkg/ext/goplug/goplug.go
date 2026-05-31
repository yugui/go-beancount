package goplug

import (
	"errors"
	"fmt"
	"plugin"
)

// APIVersion is the current goplug plugin ABI version. Plugins declare
// the version they were compiled against via [Manifest.APIVersion];
// [Load] rejects a plugin whose APIVersion does not match.
//
// Bump this value when [api.Plugin], [Manifest], or the InitPlugin
// contract changes in a backwards-incompatible way.
const APIVersion = 1

// Sentinel errors that classify every rejection path from [Load].
// Callers use [errors.Is] to distinguish them. The error returned from
// Load wraps the relevant sentinel (and, where applicable, the
// underlying stdlib error from [plugin.Open] or from InitPlugin) so a
// single [errors.Is] check covers both cases.
var (
	// ErrOpen is reported when [plugin.Open] itself fails. The
	// wrapped stdlib error gives the toolchain-/module-mismatch
	// details that Go's plugin package produces.
	ErrOpen = errors.New("open failure")
	// ErrManifestMissing is reported when the plugin does not export
	// the required Manifest symbol.
	ErrManifestMissing = errors.New("missing Manifest symbol")
	// ErrManifestWrongType is reported when the Manifest symbol
	// exists but is not of type *Manifest.
	ErrManifestWrongType = errors.New("unexpected Manifest symbol type")
	// ErrAPIVersionMismatch is reported when Manifest.APIVersion
	// does not equal [APIVersion].
	ErrAPIVersionMismatch = errors.New("API version mismatch")
	// ErrManifestNameEmpty is reported when Manifest.Name is empty.
	ErrManifestNameEmpty = errors.New("empty Manifest.Name")
	// ErrInitPluginMissing is reported when the plugin does not
	// export InitPlugin.
	ErrInitPluginMissing = errors.New("missing InitPlugin symbol")
	// ErrInitPluginWrongType is reported when InitPlugin exists but
	// is not of type func() error.
	ErrInitPluginWrongType = errors.New("unexpected InitPlugin symbol type")
	// ErrInitPluginFailed is reported when InitPlugin returns a
	// non-nil error. The plugin's own error is additionally wrapped
	// so callers can inspect it with [errors.Is]/[errors.As] when
	// they have access to the plugin's error values.
	ErrInitPluginFailed = errors.New("call to InitPlugin failed")
)

// toolchainHint is appended to the ErrOpen diagnostic because
// plugin.Open's own messages ("plugin was built with a different
// version of package X") are cryptic to operators who don't know
// about Go plugins' build-graph-parity rule.
const toolchainHint = "plugins must be built with the same Go toolchain and go-beancount module version as the host"

// Manifest is the metadata every plugin must export. [Load] reads it
// before invoking the plugin's InitPlugin, so an incompatible plugin
// is rejected without getting a chance to mutate the registry.
type Manifest struct {
	// APIVersion must equal [APIVersion]. Required.
	APIVersion int

	// Name identifies the plugin for operator diagnostics. Required —
	// Load rejects an empty Name. By convention this matches the name
	// the plugin registers itself under via postproc.Register
	// (typically a Go fully-qualified package path).
	Name string

	// Version is the plugin's own release identifier (e.g. "v1.2.3",
	// a git SHA, or a build timestamp). Informational only; the
	// loader does not interpret it.
	Version string
}

// Load opens the Go plugin at path, verifies its [Manifest], looks up
// the exported InitPlugin function, and invokes it. InitPlugin is
// responsible for calling
// [github.com/yugui/go-beancount/pkg/ext/postproc.Register] for each
// [api.Plugin] it wants to make available to the runner.
//
// Load returns an error when the file cannot be opened, the required
// symbols are missing or have unexpected types, the Manifest is
// incompatible with the host, or InitPlugin itself returns a non-nil
// error. In every rejection path before InitPlugin is called, the
// plugin gets no opportunity to touch the registry.
//
// Every returned error wraps one of the exported sentinels (ErrOpen,
// ErrManifestMissing, etc.) so callers can classify failures with
// [errors.Is] without parsing the message string.
//
// Load is not safe to call concurrently with itself or with
// postproc.Register. [plugin.Open] caches loaded files by path, so
// invoking Load twice on the same path will re-run InitPlugin and
// typically panic via postproc.Register's duplicate-name check —
// matching the established contract for init-time registration.
func Load(path string) error {
	p, err := plugin.Open(path)
	if err != nil {
		return fmt.Errorf("goplug: loading %q: %w: %w (%s)", path, ErrOpen, err, toolchainHint)
	}

	manifestSym, err := p.Lookup("Manifest")
	if err != nil {
		return fmt.Errorf("goplug: loading %q: %w: %w", path, ErrManifestMissing, err)
	}
	manifestPtr, ok := manifestSym.(*Manifest)
	if !ok {
		return fmt.Errorf("goplug: loading %q: %w: got %T, want *goplug.Manifest", path, ErrManifestWrongType, manifestSym)
	}
	if manifestPtr.APIVersion != APIVersion {
		return fmt.Errorf("goplug: loading %q: %w (plugin=%d host=%d)", path, ErrAPIVersionMismatch, manifestPtr.APIVersion, APIVersion)
	}
	if manifestPtr.Name == "" {
		return fmt.Errorf("goplug: loading %q: %w", path, ErrManifestNameEmpty)
	}

	initSym, err := p.Lookup("InitPlugin")
	if err != nil {
		return fmt.Errorf("goplug: loading %q: %w: %w", path, ErrInitPluginMissing, err)
	}
	initFn, ok := initSym.(func() error)
	if !ok {
		return fmt.Errorf("goplug: loading %q: %w: got %T, want func() error", path, ErrInitPluginWrongType, initSym)
	}
	if err := initFn(); err != nil {
		return fmt.Errorf("goplug: loading %q: %w: %w", path, ErrInitPluginFailed, err)
	}
	return nil
}

// LoadAll loads each path via [Load], skipping any path already seen so a
// path that appears more than once is loaded only once. Deduplication is
// required, not cosmetic: Load panics when the same path is loaded twice
// (plugin.Open caches the file and the second InitPlugin re-runs
// postproc.Register, tripping its duplicate-name check). The first occurrence
// of a path wins and load order follows paths.
//
// LoadAll attempts every (deduplicated) path even after a failure, so one bad
// path does not suppress the rest, and returns the joined error of all
// failures via [errors.Join] (nil when every path loaded). It inherits Load's
// concurrency contract: do not call it concurrently with itself, Load, or
// postproc.Register.
func LoadAll(paths []string) error {
	seen := make(map[string]struct{}, len(paths))
	var errs []error
	for _, p := range paths {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if err := Load(p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
