package products

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	maxEntitlementFields = 100
	maxEntitlementDepth  = 8
)

var entitlementKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type EntitlementSchema struct {
	Fields []EntitlementField `json:"fields"`
}

type EntitlementField struct {
	Key         string              `json:"key,omitempty"`
	Label       string              `json:"label,omitempty"`
	Description string              `json:"description,omitempty"`
	Type        string              `json:"type"`
	Required    bool                `json:"required,omitempty"`
	Nullable    bool                `json:"nullable,omitempty"`
	Default     any                 `json:"default,omitempty"`
	Minimum     *float64            `json:"minimum,omitempty"`
	Maximum     *float64            `json:"maximum,omitempty"`
	MinLength   *int                `json:"min_length,omitempty"`
	MaxLength   *int                `json:"max_length,omitempty"`
	MinItems    *int                `json:"min_items,omitempty"`
	MaxItems    *int                `json:"max_items,omitempty"`
	Options     []EntitlementOption `json:"options,omitempty"`
	Fields      []EntitlementField  `json:"fields,omitempty"`
	Items       *EntitlementField   `json:"items,omitempty"`
}

type EntitlementOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func ValidateEntitlementSchema(schema EntitlementSchema) error {
	count := 0
	if err := validateEntitlementFields(schema.Fields, true, 0, &count); err != nil {
		return err
	}
	return nil
}

func validateEntitlementFields(fields []EntitlementField, requireKey bool, depth int, count *int) error {
	if depth > maxEntitlementDepth {
		return errors.New("entitlement schema nesting is too deep")
	}
	seen := make(map[string]bool, len(fields))
	for i := range fields {
		field := fields[i]
		(*count)++
		if *count > maxEntitlementFields {
			return fmt.Errorf("entitlement schema cannot contain more than %d fields", maxEntitlementFields)
		}
		if requireKey {
			if !entitlementKeyPattern.MatchString(field.Key) {
				return fmt.Errorf("entitlement key %q must use lowercase letters, numbers, and underscores", field.Key)
			}
			if seen[field.Key] {
				return fmt.Errorf("entitlement key %q is duplicated", field.Key)
			}
			seen[field.Key] = true
			if strings.TrimSpace(field.Label) == "" || len([]rune(strings.TrimSpace(field.Label))) > 120 {
				return fmt.Errorf("entitlement %q label must be between 1 and 120 characters", field.Key)
			}
		}
		if len([]rune(strings.TrimSpace(field.Description))) > 500 {
			return fmt.Errorf("entitlement %q description cannot exceed 500 characters", field.Key)
		}
		if err := validateEntitlementField(field, depth, count); err != nil {
			return err
		}
	}
	return nil
}

func validateEntitlementField(field EntitlementField, depth int, count *int) error {
	switch field.Type {
	case "boolean":
	case "integer", "number":
		if field.Minimum != nil && field.Maximum != nil && *field.Minimum > *field.Maximum {
			return fmt.Errorf("entitlement %q minimum cannot exceed maximum", field.Key)
		}
	case "string":
		if err := validateLengths(field.Key, field.MinLength, field.MaxLength); err != nil {
			return err
		}
	case "select":
		if len(field.Options) == 0 {
			return fmt.Errorf("entitlement %q must define select options", field.Key)
		}
		seen := map[string]bool{}
		for _, option := range field.Options {
			if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Value) == "" {
				return fmt.Errorf("entitlement %q select options require labels and values", field.Key)
			}
			if seen[option.Value] {
				return fmt.Errorf("entitlement %q select option %q is duplicated", field.Key, option.Value)
			}
			seen[option.Value] = true
		}
	case "object":
		if err := validateEntitlementFields(field.Fields, true, depth+1, count); err != nil {
			return err
		}
	case "array":
		if field.Items == nil {
			return fmt.Errorf("entitlement %q array requires an item schema", field.Key)
		}
		if field.MinItems != nil && *field.MinItems < 0 || field.MaxItems != nil && *field.MaxItems < 0 {
			return fmt.Errorf("entitlement %q array lengths cannot be negative", field.Key)
		}
		if field.MinItems != nil && field.MaxItems != nil && *field.MinItems > *field.MaxItems {
			return fmt.Errorf("entitlement %q minimum items cannot exceed maximum items", field.Key)
		}
		item := *field.Items
		if item.Key != "" {
			return fmt.Errorf("entitlement %q array item schema cannot define a key", field.Key)
		}
		if err := validateEntitlementFields([]EntitlementField{item}, false, depth+1, count); err != nil {
			return err
		}
	default:
		return fmt.Errorf("entitlement %q has unsupported type %q", field.Key, field.Type)
	}
	if field.Default != nil {
		if err := validateEntitlementValue(field, field.Default, field.Key); err != nil {
			return fmt.Errorf("invalid default: %w", err)
		}
	}
	return nil
}

func validateLengths(key string, minimum, maximum *int) error {
	if minimum != nil && *minimum < 0 || maximum != nil && *maximum < 0 {
		return fmt.Errorf("entitlement %q string lengths cannot be negative", key)
	}
	if minimum != nil && maximum != nil && *minimum > *maximum {
		return fmt.Errorf("entitlement %q minimum length cannot exceed maximum length", key)
	}
	return nil
}

func ValidateEntitlements(schema EntitlementSchema, values map[string]any) (map[string]any, error) {
	return validateEntitlements(schema, values, true)
}

func ValidateStoredEntitlements(schema EntitlementSchema, values map[string]any) error {
	_, err := validateEntitlements(schema, values, false)
	return err
}

func validateEntitlements(schema EntitlementSchema, values map[string]any, applyDefaults bool) (map[string]any, error) {
	if values == nil {
		values = map[string]any{}
	}
	result := make(map[string]any, len(values))
	definitions := make(map[string]EntitlementField, len(schema.Fields))
	for _, field := range schema.Fields {
		definitions[field.Key] = field
	}
	for key := range values {
		if _, ok := definitions[key]; !ok {
			return nil, fmt.Errorf("entitlement %q is not defined by the product", key)
		}
	}
	for _, field := range schema.Fields {
		value, present := values[field.Key]
		if !present && applyDefaults && field.Default != nil {
			value = cloneJSONValue(field.Default)
			present = true
		}
		if !present {
			if field.Required {
				return nil, fmt.Errorf("entitlement %q is required", field.Key)
			}
			continue
		}
		normalized, err := normalizeEntitlementValue(field, value, field.Key, applyDefaults)
		if err != nil {
			return nil, err
		}
		result[field.Key] = normalized
	}
	return result, nil
}

func validateEntitlementValue(field EntitlementField, value any, path string) error {
	_, err := normalizeEntitlementValue(field, value, path, true)
	return err
}

func normalizeEntitlementValue(field EntitlementField, value any, path string, applyDefaults bool) (any, error) {
	if value == nil {
		if field.Nullable {
			return nil, nil
		}
		return nil, fmt.Errorf("entitlement %q cannot be null", path)
	}
	switch field.Type {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("entitlement %q must be boolean", path)
		}
	case "integer", "number":
		number, ok := jsonNumber(value)
		if !ok || field.Type == "integer" && math.Trunc(number) != number {
			return nil, fmt.Errorf("entitlement %q must be %s", path, field.Type)
		}
		if field.Minimum != nil && number < *field.Minimum {
			return nil, fmt.Errorf("entitlement %q must be at least %v", path, *field.Minimum)
		}
		if field.Maximum != nil && number > *field.Maximum {
			return nil, fmt.Errorf("entitlement %q must be at most %v", path, *field.Maximum)
		}
	case "string", "select":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("entitlement %q must be a string", path)
		}
		if field.MinLength != nil && len([]rune(text)) < *field.MinLength || field.MaxLength != nil && len([]rune(text)) > *field.MaxLength {
			return nil, fmt.Errorf("entitlement %q has an invalid length", path)
		}
		if field.Type == "select" {
			valid := false
			for _, option := range field.Options {
				valid = valid || option.Value == text
			}
			if !valid {
				return nil, fmt.Errorf("entitlement %q must use a defined option", path)
			}
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("entitlement %q must be an object", path)
		}
		normalized, err := validateEntitlements(EntitlementSchema{Fields: field.Fields}, object, applyDefaults)
		if err != nil {
			return nil, fmt.Errorf("entitlement %q is invalid: %w", path, err)
		}
		value = normalized
	case "array":
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("entitlement %q must be an array", path)
		}
		if field.MinItems != nil && len(items) < *field.MinItems || field.MaxItems != nil && len(items) > *field.MaxItems {
			return nil, fmt.Errorf("entitlement %q has an invalid number of items", path)
		}
		normalized := make([]any, 0, len(items))
		for index, item := range items {
			normalizedItem, err := normalizeEntitlementValue(*field.Items, item, fmt.Sprintf("%s[%d]", path, index), applyDefaults)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, normalizedItem)
		}
		value = normalized
	}
	return value, nil
}

func jsonNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func cloneJSONValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return value
	}
	return cloned
}
