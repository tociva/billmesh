package metering

import (
	"errors"
	"math/big"
)

type Rate struct{ Numerator, Denominator int64 }

func Credits(quantity int64, rate Rate) (int64, error) {
	if quantity < 0 || rate.Numerator < 0 || rate.Denominator <= 0 {
		return 0, errors.New("invalid quantity or rate")
	}
	value := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(rate.Numerator))
	den := big.NewInt(rate.Denominator)
	value.Add(value, new(big.Int).Sub(den, big.NewInt(1)))
	value.Div(value, den)
	if !value.IsInt64() {
		return 0, errors.New("credit calculation overflow")
	}
	return value.Int64(), nil
}
func Price(meter string, quantity int64, rates map[string]Rate) (int64, error) {
	rate, ok := rates[meter]
	if !ok {
		return 0, errors.New("unsupported meter")
	}
	return Credits(quantity, rate)
}
