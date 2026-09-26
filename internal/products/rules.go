package products

import (
	"errors"
	"strings"
)

type PlanInput struct {
	PriceMinor, IncludedCredits int64
	Currency                    string
}

func ValidatePlan(v PlanInput) error {
	if v.PriceMinor < 0 {
		return errors.New("price cannot be negative")
	}
	if v.IncludedCredits < 0 {
		return errors.New("included credits cannot be negative")
	}
	if len(v.Currency) != 3 || v.Currency != strings.ToUpper(v.Currency) {
		return errors.New("currency must be a three-letter uppercase code")
	}
	return nil
}
