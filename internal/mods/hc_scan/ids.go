package hcscan

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultIDPattern matches the employee code format the Python code validates when a member is added
// (^[A-Z]{2,3}\d{2}\w\d{2}$): two or three letters, two digits, one letter or digit, two digits.
const DefaultIDPattern = `[A-Z]{2,3}\d{2}[A-Z0-9]\d{2}`

// IDParser finds an employee code inside whatever a barcode scanner typed. Scanners may add prefix or suffix
// characters, may or may not press Enter and may type lower case, so the code is searched, not compared.
type IDParser struct {
	pattern string
	re      *regexp.Regexp
}

// NewIDParser compiles the pattern case-insensitively.
func NewIDParser(pattern string) (*IDParser, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = DefaultIDPattern
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("employee_id_pattern is not a valid regular expression: %w", err)
	}
	return &IDParser{pattern: pattern, re: re}, nil
}

// Pattern returns the pattern source, which the browser uses with the same meaning.
func (p *IDParser) Pattern() string { return p.pattern }

// Extract returns the first employee code found in raw, upper-cased.
func (p *IDParser) Extract(raw string) (string, bool) {
	m := p.re.FindString(strings.TrimSpace(raw))
	if m == "" {
		return "", false
	}
	return strings.ToUpper(m), true
}
