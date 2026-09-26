package subscriptions

import (
	"fmt"
	"time"
)

func Period(start time.Time, interval string) (time.Time, time.Time, error) {
	start = start.UTC()
	switch interval {
	case "monthly":
		return start, start.AddDate(0, 1, 0), nil
	case "annual":
		return start, start.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported interval %q", interval)
	}
}
