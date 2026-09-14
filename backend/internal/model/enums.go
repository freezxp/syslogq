package model

// Severity names per RFC5424 §6.2.1 (docs/log-data-model.md §4).
var severityNamesRFC5424 = [8]string{
	"emergency", "alert", "critical", "error",
	"warning", "notice", "info", "debug",
}

// Severity names per RFC3164 convention (doc §4): the same 0–7 codes carry
// the historical BSD labels.
var severityNamesRFC3164 = [8]string{
	"panic", "alert", "crit", "err",
	"warning", "notice", "info", "debug",
}

// SeverityNameRFC5424 maps a severity code to its RFC5424 name; "" if out of range.
func SeverityNameRFC5424(sev int) string {
	if sev < 0 || sev > 7 {
		return ""
	}
	return severityNamesRFC5424[sev]
}

// SeverityNameRFC3164 maps a severity code to its RFC3164-era name; "" if out of range.
func SeverityNameRFC3164(sev int) string {
	if sev < 0 || sev > 7 {
		return ""
	}
	return severityNamesRFC3164[sev]
}

// Facility names, slots 0–15 per the classic syslog tables (RFC5424 §6.2.1
// with the traditional aliases), 16–23 local0–local7 (doc §4).
var facilityNames = [24]string{
	"kern", "user", "mail", "daemon",
	"auth", "syslog", "lpr", "news",
	"uucp", "cron", "authpriv", "ftp",
	"ntp", "security", "console", "clock",
	"local0", "local1", "local2", "local3",
	"local4", "local5", "local6", "local7",
}

// FacilityName maps a facility code to its name; "" if out of range.
func FacilityName(fac int) string {
	if fac < 0 || fac >= len(facilityNames) {
		return ""
	}
	return facilityNames[fac]
}
