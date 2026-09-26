package payments

import "errors"

type CreditPack struct {
	ID                  string
	Credits, PriceMinor int64
	Currency            string
}

func ResolvePack(id string, packs map[string]CreditPack) (CreditPack, error) {
	pack, ok := packs[id]
	if !ok || pack.Credits <= 0 || pack.PriceMinor < 0 {
		return CreditPack{}, errors.New("unknown or invalid credit pack")
	}
	return pack, nil
}
