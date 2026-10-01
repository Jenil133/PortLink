package lttb

import "math"

// Indices returns the indices of the points to keep when downsampling the series (xs, ys)
// to at most `threshold` points with Largest-Triangle-Three-Buckets.
// The first and last points are always kept. xs must be increasing.
func Indices(xs, ys []float64, threshold int) []int {
	n := len(xs)
	if threshold >= n || threshold < 3 {
		all := make([]int, n)
		for i := range all {
			all[i] = i
		}
		return all
	}
	out := make([]int, 0, threshold)
	out = append(out, 0)
	every := float64(n-2) / float64(threshold-2) // bucket width in points
	a := 0                                       // index of the previously selected point
	for i := 0; i < threshold-2; i++ {
		// Average of the NEXT bucket: the third corner of the triangle.
		avgStart := int(float64(i+1)*every) + 1
		avgEnd := int(float64(i+2)*every) + 1
		if avgEnd > n {
			avgEnd = n
		}
		var avgX, avgY float64
		for j := avgStart; j < avgEnd; j++ {
			avgX += xs[j]
			avgY += ys[j]
		}
		cnt := float64(avgEnd - avgStart)
		avgX /= cnt
		avgY /= cnt

		// In the CURRENT bucket keep the point that forms the largest triangle with
		// the previous pick and the next bucket's average.
		start := int(float64(i)*every) + 1
		end := int(float64(i+1)*every) + 1
		maxArea, pick := -1.0, start
		for j := start; j < end; j++ {
			area := math.Abs((xs[a]-avgX)*(ys[j]-ys[a]) - (xs[a]-xs[j])*(avgY-ys[a]))
			if area > maxArea {
				maxArea, pick = area, j
			}
		}
		out = append(out, pick)
		a = pick
	}
	return append(out, n-1)
}
