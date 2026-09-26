package notifications

func UsagePercent(used, limit int64) int {
	if used <= 0 {
		return 0
	}
	if limit <= 0 {
		return 100
	}
	if used >= limit {
		return 100
	}
	return int(used * 100 / limit)
}
