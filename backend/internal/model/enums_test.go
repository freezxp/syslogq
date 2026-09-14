package model

import "testing"

func TestSeverityNames(t *testing.T) {
	cases := []struct {
		sev          int
		rfc5424, bsd string
	}{
		{0, "emergency", "panic"},
		{3, "error", "err"},
		{4, "warning", "warning"},
		{7, "debug", "debug"},
		{8, "", ""},
		{-1, "", ""},
	}
	for _, c := range cases {
		if got := SeverityNameRFC5424(c.sev); got != c.rfc5424 {
			t.Errorf("SeverityNameRFC5424(%d) = %q, want %q", c.sev, got, c.rfc5424)
		}
		if got := SeverityNameRFC3164(c.sev); got != c.bsd {
			t.Errorf("SeverityNameRFC3164(%d) = %q, want %q", c.sev, got, c.bsd)
		}
	}
}

func TestFacilityNames(t *testing.T) {
	cases := map[int]string{
		0: "kern", 1: "user", 3: "daemon", 4: "auth", 9: "cron",
		10: "authpriv", 11: "ftp", 12: "ntp", 16: "local0", 23: "local7",
		24: "", -1: "",
	}
	for fac, want := range cases {
		if got := FacilityName(fac); got != want {
			t.Errorf("FacilityName(%d) = %q, want %q", fac, got, want)
		}
	}
}
