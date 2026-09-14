package parser

// Detect sniffs the wire format of a raw payload (docs/ingestion.md §2):
// RFC5424 ("<PRI>VERSION SP") → RFC3164 ("<PRI>" without a version) → unknown.
// Detection is cheap and never panics; it cannot fail.
func Detect(raw []byte) string {
	if len(raw) == 0 || raw[0] != '<' {
		return FormatUnknown
	}
	i := 1
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	if i == 1 || i >= len(raw) || raw[i] != '>' {
		return FormatUnknown
	}
	rest := raw[i+1:]
	// RFC5424 requires a non-zero version followed by a space right after PRI.
	if len(rest) >= 2 && rest[0] >= '1' && rest[0] <= '9' {
		j := 1
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j <= 3 && j < len(rest) && rest[j] == ' ' {
			return FormatRFC5424
		}
	}
	// "<PRI>" followed by anything else: classic BSD syslog.
	return FormatRFC3164
}
