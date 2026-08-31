package diagnostics

import "regexp"

var (
	redactBearer      = regexp.MustCompile(`(?i)(Bearer\s+)[^\s,;]+`)
	redactURLUserInfo = regexp.MustCompile(`(?i)(https?://)[^\s/@:]+:[^\s/@]+@`)
	redactAssignment  = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|authorization|private[_-]?key)\s*[:=]\s*)("[^"]*"|'[^']*'|[^,\s;]+)`)
	redactQueryValue  = regexp.MustCompile(`([?&][^=\s&#]+)=([^&#\s]+)`)
)

// RedactText removes common credential-bearing forms before text is allowed
// into durable diagnostics. This is intentionally conservative: structured
// attributes are allowlisted separately by Handler, while messages receive
// masking and a hard size bound at the store boundary.
func RedactText(value string) string {
	value = redactBearer.ReplaceAllString(value, `${1}[REDACTED]`)
	value = redactURLUserInfo.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = redactAssignment.ReplaceAllString(value, `${1}[REDACTED]`)
	value = redactQueryValue.ReplaceAllString(value, `${1}=[REDACTED]`)
	return value
}
