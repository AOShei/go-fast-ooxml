package formula

import "testing"

// The expected formulas are what openpyxl 3.1.5's Translator gives for the
// same move (bench-env/.venv-ooxml); cases where a reference leaves the
// sheet are tested apart, below.
func TestShiftAsOpenpyxl(t *testing.T) {
	for _, c := range []struct {
		f          string
		rows, cols int
		want       string
	}{
		{"A1*2", 3, 2, "C4*2"},
		{"$A$1+A$1+$A1+A1", 3, 2, "$A$1+C$1+$A4+C4"},
		{"SUM(A1:B2)", 3, 2, "SUM(C4:D5)"},
		{"SUM($A$1:B2)", 3, 2, "SUM($A$1:D5)"},
		{"Sheet2!D4+'My sheet'!E5", 3, 2, "Sheet2!F7+'My sheet'!G8"},
		{"Sheet2!D4+'My sheet'!E5", -3, -2, "Sheet2!B1+'My sheet'!C2"},
		{"'It''s'!A1", 3, 2, "'It''s'!C4"},
		{"A:A", 3, 2, "C:C"},
		{"$A:B", 3, 2, "$A:D"},
		{"3:3", 3, 2, "6:6"},
		{"1:$2", 3, 2, "4:$2"},
		{"\"A1\"&A1", 3, 2, "\"A1\"&C4"},
		{"\"say \"\"B2\"\"\"&B2", 3, 2, "\"say \"\"B2\"\"\"&D5"},
		{"LOG10(A1)", 3, 2, "LOG10(C4)"},
		{"Table1[Col]+A1", 3, 2, "Table1[Col]+C4"},
		{"Table1[[#This Row],[Amount]]*B3", 3, 2, "Table1[[#This Row],[Amount]]*D6"},
		{"A1:A2 B1:B2", 3, 2, "C4:C5 D4:D5"},
		{"IF(TRUE,A1,FALSE)", 3, 2, "IF(TRUE,C4,FALSE)"},
		{"1E5+A1", 3, 2, "1E5+C4"},
		{"1.5E-3*C3", 3, 2, "1.5E-3*E6"},
		{"_xlfn.STDEV.S(A1:A9)", 3, 2, "_xlfn.STDEV.S(C4:C12)"},
		{"INDIRECT(\"R1C1\",FALSE)+R1C1", 3, 2, "INDIRECT(\"R1C1\",FALSE)+R1C1"},
		{"INDIRECT(\"R1C1\",FALSE)+R1C1", -3, -2, "INDIRECT(\"R1C1\",FALSE)+R1C1"},
		{"[1]Sheet1!A1+B1", 3, 2, "[1]Sheet1!C4+D4"},
		{"VLOOKUP(A2,$H$2:$J$100,3,FALSE)", 3, 2, "VLOOKUP(C5,$H$2:$J$100,3,FALSE)"},
		{"#REF!+A1", 3, 2, "#REF!+C4"},
		{"#N/A", 3, 2, "#N/A"},
		{"#N/A", -3, -2, "#N/A"},
		{"SUMIF(A:A,\">5\",B:B)", 3, 2, "SUMIF(C:C,\">5\",D:D)"},
		{"a1+b2", 3, 2, "C4+D5"},
		{"Rate*A1", 3, 2, "Rate*C4"},
		{"-A1^2%", 3, 2, "-C4^2%"},
		{"{1,2;3,4}", 3, 2, "{1,2;3,4}"},
		{"{1,2;3,4}", -3, -2, "{1,2;3,4}"},
		{"OFFSET(A1,1,1)", 3, 2, "OFFSET(C4,1,1)"},
		{"A1&\"'\"&B1", 3, 2, "C4&\"'\"&D4"},
	} {
		if got := Shift(c.f, c.rows, c.cols); got != c.want {
			t.Errorf("Shift(%q, %d, %d) = %q, want %q", c.f, c.rows, c.cols, got, c.want)
		}
	}
}

// A reference moved off the sheet is #REF!, as in Excel; a fixed one stays.
func TestShiftOffTheSheet(t *testing.T) {
	for _, c := range []struct{ f, want string }{
		{"A1+$A$1", "#REF!+$A$1"},
		{"SUM(A1:B2)", "SUM(#REF!)"},
		{"A:A", "#REF!"},
		{"1:1", "#REF!"},
	} {
		if got := Shift(c.f, -1, -1); got != c.want {
			t.Errorf("Shift(%q, -1, -1) = %q, want %q", c.f, got, c.want)
		}
	}
	// Past the last column and the last row (openpyxl does not stop
	// there).
	if got := Shift("XFD1+A1048576", 1, 1); got != "#REF!+#REF!" {
		t.Errorf("past the end: %q", got)
	}
	for _, c := range []struct{ f, want string }{
		{"A1", "A1"},
	} {
		if got := Shift(c.f, 0, 0); got != c.want {
			t.Errorf("Shift(%q, -1, -1) = %q, want %q", c.f, got, c.want)
		}
	}
}

func FuzzShift(f *testing.F) {
	for _, s := range []string{"A1*2", "SUM($A$1:B2)", "'It''s'!A1+\"x\"", "Table1[[#This Row],[A]]", "A:A+1:1", "#REF!"} {
		f.Add(s, 3, -2)
	}
	f.Fuzz(func(t *testing.T, s string, rows, cols int) {
		rows, cols = rows%2000000, cols%40000
		out := Shift(s, rows, cols)
		if rows == 0 && cols == 0 && out != s {
			t.Fatalf("no move changed %q to %q", s, out)
		}
		_ = Shift(out, -rows, -cols)
	})
}
