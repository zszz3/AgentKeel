package fixtest

import "sort"

// Median 返回 xs 的中位数，xs 为空时返回 0。不修改 xs。
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	return xs[len(xs)/2]
}
