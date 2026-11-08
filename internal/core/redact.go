package core

import "regexp"

var uuid = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var credential = regexp.MustCompile(`(?i)(password|access[_-]?key|token|secret|authorization|uuid)(["'\s:=]+)[^\s,;}"']+`)
var uri = regexp.MustCompile(`(?i)\b(?:vless|vmess|trojan|ss|hysteria2?|tuic)://[^\s]+`)

func SafeText(s string) string {
	s = uri.ReplaceAllString(s, "[redacted URI]")
	s = uuid.ReplaceAllString(s, "[redacted UUID]")
	return credential.ReplaceAllString(s, "$1$2[redacted]")
}
