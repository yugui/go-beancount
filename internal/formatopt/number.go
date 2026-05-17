package formatopt

import "strings"

// StripCommas removes commas from a number string.
func StripCommas(s string) string {
	return strings.ReplaceAll(s, ",", "")
}

// InsertCommas adds thousand-separator commas to the integer part of a number.
func InsertCommas(s string) string {
	// Handle negative sign.
	neg := false
	num := s
	if len(num) > 0 && num[0] == '-' {
		neg = true
		num = num[1:]
	}

	// Strip existing commas first.
	num = strings.ReplaceAll(num, ",", "")

	// Split at decimal point.
	intPart := num
	decPart := ""
	if i := strings.IndexByte(num, '.'); i >= 0 {
		intPart = num[:i]
		decPart = num[i:]
	}

	// Insert commas in the integer part.
	if len(intPart) > 3 {
		var b strings.Builder
		remainder := len(intPart) % 3
		if remainder > 0 {
			b.WriteString(intPart[:remainder])
		}
		for i := remainder; i < len(intPart); i += 3 {
			if b.Len() > 0 {
				b.WriteByte(',')
			}
			b.WriteString(intPart[i : i+3])
		}
		intPart = b.String()
	}

	result := intPart + decPart
	if neg {
		result = "-" + result
	}
	return result
}
