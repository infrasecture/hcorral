package update

import "testing"

func TestCompare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want int
	}{{"0.147.0", "0.148.0", -1}, {"1.0.0", "1.0.0", 0}, {"1.0.0", "1.0.0-rc.1", 1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1}}
	for _, test := range tests {
		a, e := Parse(test.a)
		if e != nil {
			t.Fatal(e)
		}
		b, e := Parse(test.b)
		if e != nil {
			t.Fatal(e)
		}
		if got := Compare(a, b); got != test.want {
			t.Errorf("Compare(%s,%s)=%d", test.a, test.b, got)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"0.147.0", "1.0.0-rc.1", "v2.3.4+build", "", "01.2.3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		version, err := Parse(value)
		if err == nil {
			if version.Major < 0 || version.Minor < 0 || version.Patch < 0 {
				t.Fatal("negative parsed version")
			}
		}
	})
}
