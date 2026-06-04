package csvkit_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/yugui/go-beancount/pkg/importer/std/csvkit"
)

func collect(t *testing.T, r *csvkit.Reader, body string) ([]string, []csvkit.Record) {
	t.Helper()
	header, rows, err := r.Records(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Records() err = %v", err)
	}
	var recs []csvkit.Record
	for rec, rerr := range rows {
		if rerr != nil {
			t.Fatalf("iteration err = %v", rerr)
		}
		recs = append(recs, rec)
	}
	return header, recs
}

func TestReaderRecords(t *testing.T) {
	header, recs := collect(t, &csvkit.Reader{}, "A,B\n1,2\n3,4\n")
	if diff := cmp.Diff([]string{"A", "B"}, header); diff != "" {
		t.Errorf("header mismatch (-want +got):\n%s", diff)
	}
	want := []csvkit.Record{
		{Fields: []string{"1", "2"}, Line: 2},
		{Fields: []string{"3", "4"}, Line: 3},
	}
	if diff := cmp.Diff(want, recs); diff != "" {
		t.Errorf("records mismatch (-want +got):\n%s", diff)
	}
}

func TestReaderStripsBOM(t *testing.T) {
	const bom = "\ufeff"
	header, _ := collect(t, &csvkit.Reader{}, bom+"Date,Amount\n2024-01-01,1\n")
	if header[0] != "Date" {
		t.Errorf("header[0] = %q, want %q (BOM not stripped)", header[0], "Date")
	}
}

func TestReaderSkipLines(t *testing.T) {
	_, recs := collect(t, &csvkit.Reader{SkipLines: 2}, "banner1\nbanner2\nA,B\n1,2\n")
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	// Line counts banner lines: header is line 3, first data row line 4.
	if recs[0].Line != 4 {
		t.Errorf("Line = %d, want 4", recs[0].Line)
	}
}

func TestReaderHeaderless(t *testing.T) {
	r := &csvkit.Reader{Columns: map[string]int{"Date": 0, "Amount": 1}}
	header, recs := collect(t, r, "2024-01-01,10\n2024-01-02,20\n")
	if header != nil {
		t.Errorf("header = %v, want nil in headerless mode", header)
	}
	want := []csvkit.Record{
		{Fields: []string{"2024-01-01", "10"}, Line: 1},
		{Fields: []string{"2024-01-02", "20"}, Line: 2},
	}
	if diff := cmp.Diff(want, recs); diff != "" {
		t.Errorf("records mismatch (-want +got):\n%s", diff)
	}
}

func TestReaderHeaderMatch(t *testing.T) {
	hasCols := func(want ...string) func([]string) bool {
		return func(row []string) bool {
			set := map[string]bool{}
			for _, c := range row {
				set[c] = true
			}
			for _, w := range want {
				if !set[w] {
					return false
				}
			}
			return true
		}
	}
	r := &csvkit.Reader{HeaderMatch: hasCols("Date", "Amount")}
	body := "Bank statement\n\"period: 2024\"\nDate,Amount\n2024-01-01,10\n"
	header, recs := collect(t, r, body)
	if diff := cmp.Diff([]string{"Date", "Amount"}, header); diff != "" {
		t.Errorf("header mismatch (-want +got):\n%s", diff)
	}
	if len(recs) != 1 || recs[0].Line != 4 {
		t.Fatalf("recs = %+v, want one record at line 4", recs)
	}
}

func TestReaderHeaderMatchNotFound(t *testing.T) {
	r := &csvkit.Reader{HeaderMatch: func([]string) bool { return false }}
	_, _, err := r.Records(strings.NewReader("a\nb\n"))
	if err == nil {
		t.Error("Records() err = nil, want error when no header matches")
	}
}

func TestReaderColumnsAndHeaderMatchConflict(t *testing.T) {
	r := &csvkit.Reader{
		Columns:     map[string]int{"A": 0},
		HeaderMatch: func([]string) bool { return true },
	}
	if _, _, err := r.Records(strings.NewReader("x\n")); err == nil {
		t.Error("Records() err = nil, want error for conflicting Columns and HeaderMatch")
	}
}

func TestReaderTSV(t *testing.T) {
	header, recs := collect(t, &csvkit.Reader{Delimiter: '\t'}, "A\tB\n1\t2\n")
	if diff := cmp.Diff([]string{"A", "B"}, header); diff != "" {
		t.Errorf("header mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"1", "2"}, recs[0].Fields); diff != "" {
		t.Errorf("fields mismatch (-want +got):\n%s", diff)
	}
}
