package invoices

import (
	"errors"
	"math"
)

type Line struct{ Quantity, UnitPriceMinor int64 }

func Total(lines []Line, precision int) (int64, error) {
	if precision < 0 || precision > 3 {
		return 0, errors.New("invalid currency precision")
	}
	var total int64
	for _, line := range lines {
		if line.Quantity < 0 || line.UnitPriceMinor < 0 {
			return 0, errors.New("negative invoice value")
		}
		if line.Quantity != 0 && line.UnitPriceMinor > math.MaxInt64/line.Quantity {
			return 0, errors.New("invoice total overflow")
		}
		amount := line.Quantity * line.UnitPriceMinor
		if total > math.MaxInt64-amount {
			return 0, errors.New("invoice total overflow")
		}
		total += amount
	}
	return total, nil
}
