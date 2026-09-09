package arca

import "strings"

// IsCanonicalMarketID validates the platform identifier shape. It does not
// resolve a display symbol or prove current account tradability.
func IsCanonicalMarketID(id string) bool {
	id = strings.TrimSpace(id)
	if strings.ContainsAny(id, " \t\n") {
		return false
	}
	parts := strings.Split(id, ":")
	digits := func(s string) bool {
		if s == "" {
			return false
		}
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	switch len(parts) {
	case 2:
		return parts[0] == "gllt" && digits(parts[1])
	case 3:
		return parts[0] != "" && parts[0] != "gllt" && parts[2] != "" && digits(parts[1])
	default:
		return false
	}
}
