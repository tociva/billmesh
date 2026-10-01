package products

import (
	"errors"
	"regexp"
	"strings"
)

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type PlanInput struct {
	PriceMinor, IncludedCredits int64
	Currency, BillingInterval   string
}

func ValidatePlan(v PlanInput) error {
	if v.PriceMinor < 0 {
		return errors.New("price cannot be negative")
	}
	if v.IncludedCredits < 0 {
		return errors.New("included credits cannot be negative")
	}
	if !currencyPattern.MatchString(v.Currency) {
		return errors.New("currency must be a three-letter uppercase code")
	}
	if v.BillingInterval != "" && v.BillingInterval != "monthly" && v.BillingInterval != "annual" {
		return errors.New("billing interval must be monthly or annual")
	}
	return nil
}

func ValidatePlanName(name string) error {
	length := len([]rune(strings.TrimSpace(name)))
	if length < 1 || length > 120 {
		return errors.New("name must be between 1 and 120 characters")
	}
	return nil
}

func ValidateProduct(slug, name, description string) error {
	if len(slug) < 2 || len(slug) > 63 || !slugPattern.MatchString(slug) {
		return errors.New("slug must be 2-63 lowercase letters, numbers, or single hyphens")
	}
	if strings.TrimSpace(name) == "" || len([]rune(strings.TrimSpace(name))) > 120 {
		return errors.New("name must be between 1 and 120 characters")
	}
	if len([]rune(strings.TrimSpace(description))) > 2000 {
		return errors.New("description cannot exceed 2000 characters")
	}
	return nil
}

func ValidatePlanSlug(slug string) error {
	if len(slug) < 2 || len(slug) > 63 || !slugPattern.MatchString(slug) {
		return errors.New("slug must be 2-63 lowercase letters, numbers, or single hyphens")
	}
	return nil
}
