package domain

import "strings"

// Currency is an ISO 4217 code such as "THB" or "USD".
//
// Amounts everywhere in this service are int64 values in the currency's
// minor unit (satang, cents, ...). Never use float64 for money.
type Currency string

func ParseCurrency(s string) (Currency, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 3 {
		return "", ErrInvalidCurrency
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return "", ErrInvalidCurrency
		}
	}
	return Currency(s), nil
}
