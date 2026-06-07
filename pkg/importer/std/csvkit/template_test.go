package csvkit_test

import (
	"testing"

	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func TestNarrationTemplate(t *testing.T) {
	nt, err := csvkit.CompileNarration(`{{.Payee}}{{if .Memo}} ({{.Memo}}){{end}}`)
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}

	cases := []struct {
		name string
		data map[string]string
		want string
	}{
		{name: "with memo", data: map[string]string{"Payee": "Cafe", "Memo": "latte"}, want: "Cafe (latte)"},
		{name: "blank memo drops branch", data: map[string]string{"Payee": "Cafe", "Memo": ""}, want: "Cafe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nt.Render(tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tc.want {
				t.Errorf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNarrationTemplateFuncs(t *testing.T) {
	nt, err := csvkit.CompileNarration(`{{upper (trim .Desc)}}/{{default "n/a" .Note}}`)
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	got, err := nt.Render(map[string]string{"Desc": "  buy  ", "Note": ""})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "BUY/n/a"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestNarrationTemplateCompileError(t *testing.T) {
	if _, err := csvkit.CompileNarration(`{{.Unterminated`); err == nil {
		t.Error("CompileNarration() err = nil, want parse error")
	}
}

func TestNarrationTemplateMissingKeyError(t *testing.T) {
	nt, err := csvkit.CompileNarration(`{{.Unknown}}`)
	if err != nil {
		t.Fatalf("CompileNarration: %v", err)
	}
	if _, err := nt.Render(map[string]string{"Payee": "x"}); err == nil {
		t.Error("Render() err = nil for unknown key, want error")
	}
}
