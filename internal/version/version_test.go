package version

import "testing"

func TestChannel(t *testing.T) {
	for v, want := range map[string]string{
		"0.1.0":                               "beta",
		"0.1.0+dirty":                         "dev",
		"0.1.1-0.20260925184804-3bf0dd25927c": "dev",
		"0.0.0-dev":                           "dev",
		"0.2.0-SNAPSHOT-abc":                  "dev",
		"1.0.0-rc1":                           "rc",
		"1.2.3":                               "stable",
	} {
		if got := channel(v); got != want {
			t.Errorf("channel(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestString(t *testing.T) {
	if got := (Info{Version: "0.1.0", Channel: "beta"}).String(); got != "v0.1.0 (beta)" {
		t.Errorf("String() = %q", got)
	}
	if got := (Info{Version: "1.0.0", Channel: "stable"}).String(); got != "v1.0.0" {
		t.Errorf("String() = %q", got)
	}
}
