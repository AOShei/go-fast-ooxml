package formula

import "testing"

func TestFromR1C1(t *testing.T) {
	// The formulas are in C5: row 4, column 2 from 0.
	for f, want := range map[string]string{
		"=R[-1]C+1":                        "=C4+1",
		"=SUM(R1C1:R3C2)":                  "=SUM($A$1:$B$3)",
		"=RC[-2]*R2C":                      "=A5*C$2",
		"=R[1]C[1]":                        "=D6",
		"=SUM(R)":                          "=SUM(5:5)",
		"=SUM(C)":                          "=SUM(C:C)",
		"=SUM(R2:R4)":                      "=SUM($2:$4)",
		"=SUM(C[-1]:C)":                    "=SUM(B:C)",
		"=COUNT(C1:C3)":                    "=COUNT($A:$C)",
		"=ROUND(RC[-1],2)":                 "=ROUND(B5,2)",
		"=Sheet2!R1C1":                     "=Sheet2!$A$1",
		"='My Sheet'!R[0]C[0]":             "='My Sheet'!C5",
		"=\"R1C1\"&RC":                     "=\"R1C1\"&C5",
		"=[Book1.xls]Sheet1!R2C2":          "=[Book1.xls]Sheet1!$B$2",
		"=R[-5]C":                          "=#REF!",
		"=Rate*Cost":                       "=Rate*Cost",
		"=R1C1C":                           "=R1C1C",
		"=IF(R[-1]C>0,\"yes\",#N/A)":       "=IF(C4>0,\"yes\",#N/A)",
		"=R1048576C16384":                  "=$XFD$1048576",
		"=R1048577C1":                      "=#REF!",
		"=SUM(R1C1:R2C2 R2C2:R3C3)":        "=SUM($A$1:$B$2 $B$2:$C$3)",
		"=OFFSET(RC,1,1)+INDIRECT(\"RC\")": "=OFFSET(C5,1,1)+INDIRECT(\"RC\")",
	} {
		if got := FromR1C1(f, 4, 2); got != want {
			t.Errorf("%s: %s, want %s", f, got, want)
		}
	}
}

func TestR1C1Range(t *testing.T) {
	for s, want := range map[string][4]int{
		"R2C1:R10C1": {1, 0, 9, 0},
		"R1C1":       {0, 0, 0, 0},
		"C3":         {0, 2, maxRow - 1, 2},
		"R2:R3":      {1, 0, 2, maxCol - 1},
		"RC[1]":      {4, 3, 4, 3},
	} {
		r0, c0, r1, c1, ok := R1C1Range(s, 4, 2)
		if !ok || [4]int{r0, c0, r1, c1} != want {
			t.Errorf("%s: %v %v, want %v", s, [4]int{r0, c0, r1, c1}, ok, want)
		}
	}
	for _, s := range []string{"", "A1", "R1C1:", "R0C1", "R1C1,R2C2"} {
		if _, _, _, _, ok := R1C1Range(s, 0, 0); ok {
			t.Errorf("%q read as a range", s)
		}
	}
}

func FuzzFromR1C1(f *testing.F) {
	f.Add("=SUM(R1C1:R[2]C[-1])&\"x\"")
	f.Fuzz(func(t *testing.T, s string) {
		FromR1C1(s, 3, 3)
		R1C1Range(s, 3, 3)
	})
}
