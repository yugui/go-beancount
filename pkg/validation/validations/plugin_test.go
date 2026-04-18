package validations_test

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/yugui/go-beancount/pkg/ast"
	"github.com/yugui/go-beancount/pkg/postproc/api"
	"github.com/yugui/go-beancount/pkg/validation/validations"
)

// seqOf adapts a slice of directives into an iter.Seq2[int, ast.Directive]
// compatible with api.Input.Directives without allocating a full ast.Ledger.
func seqOf(directives []ast.Directive) iter.Seq2[int, ast.Directive] {
	return func(yield func(int, ast.Directive) bool) {
		for i, d := range directives {
			if !yield(i, d) {
				return
			}
		}
	}
}

func TestPlugin_Name_Stable(t *testing.T) {
	got := validations.Plugin{}.Name()
	want := "github.com/yugui/go-beancount/pkg/validation/validations"
	if got != want {
		t.Errorf("Plugin{}.Name() = %q, want %q", got, want)
	}
}

func TestPlugin_EmptyLedger(t *testing.T) {
	res, err := validations.Plugin{}.Apply(context.Background(), api.Input{})
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if res.Directives != nil {
		t.Errorf("Result.Directives = %v, want nil (plugin does not mutate the ledger)", res.Directives)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty", res.Errors)
	}
}

func TestPlugin_NoValidatorsNoErrors(t *testing.T) {
	open := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{open}),
	}
	res, err := validations.Plugin{}.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Result.Errors = %v, want empty (no validators registered)", res.Errors)
	}
}

func TestPlugin_DuplicateOpen(t *testing.T) {
	// Two Open directives for the same account; the second must surface
	// as a single duplicate-open diagnostic via the openClose validator.
	d1 := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 1, Offset: 0}},
	}
	d2 := &ast.Open{
		Date:    time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Filename: "l.beancount", Line: 2, Offset: 40}},
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{d1, d2}),
	}
	res, err := validations.Plugin{}.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "duplicate-open" {
		t.Errorf("Code = %q, want %q", e.Code, "duplicate-open")
	}
	if e.Span != d2.Span {
		t.Errorf("Span = %#v, want the second Open's span %#v", e.Span, d2.Span)
	}
	if want := `account "Assets:Cash" already opened`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestPlugin_ReferenceBeforeOpen(t *testing.T) {
	// Balance dated 2023-12-31 against an account opened 2024-01-01 must
	// emit exactly one account-not-yet-open diagnostic.
	open := &ast.Open{
		Date:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	bal := &ast.Balance{
		Date:    time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 2, Offset: 30}},
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{open, bal}),
	}
	res, err := validations.Plugin{}.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "account-not-yet-open" {
		t.Errorf("Code = %q, want %q", e.Code, "account-not-yet-open")
	}
	if e.Span != bal.Span {
		t.Errorf("Span = %#v, want balance span %#v", e.Span, bal.Span)
	}
	if want := `account "Assets:Cash" is not open on 2023-12-31`; e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestPlugin_ReferenceOnUnopenedAccount(t *testing.T) {
	// Balance referencing an account that has never been opened.
	bal := &ast.Balance{
		Date:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		Account: "Assets:Cash",
		Span:    ast.Span{Start: ast.Position{Line: 1}},
	}
	in := api.Input{
		Directives: seqOf([]ast.Directive{bal}),
	}
	res, err := validations.Plugin{}.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	if got, want := res.Errors[0].Code, "account-not-open"; got != want {
		t.Errorf("Code = %q, want %q", got, want)
	}
}

func TestPlugin_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := validations.Plugin{}.Apply(ctx, api.Input{})
	if err == nil {
		t.Fatalf("Apply on canceled ctx returned nil error, want non-nil")
	}
}

func TestPlugin_OptionsFromRawParseError(t *testing.T) {
	// "inferred_tolerance_multiplier" is a registered decimal-valued
	// option; a non-numeric value triggers a ParseError which the
	// plugin surfaces as api.Error{Code: "invalid-option"}.
	in := api.Input{
		Options: map[string]string{
			"inferred_tolerance_multiplier": "not-a-decimal",
		},
	}
	res, err := validations.Plugin{}.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("len(Result.Errors) = %d, want 1; errors = %v", len(res.Errors), res.Errors)
	}
	e := res.Errors[0]
	if e.Code != "invalid-option" {
		t.Errorf("Error.Code = %q, want %q", e.Code, "invalid-option")
	}
	if !strings.Contains(e.Message, "inferred_tolerance_multiplier") {
		t.Errorf("Error.Message = %q, want it to mention the option key", e.Message)
	}
}
