package stats

import "math"

// StandardDeviation is the struct to calculate and store standard deviation
// specifically this is a population standard deviation
type StandardDeviation struct {
	Name    string
	Sum     float64
	Mean    float64
	Buckets []float64
	Std     float64 // σ
}

// NewStandardDeviation creates a new standard dev object
func NewStandardDeviation(name string) StandardDeviation {
	return StandardDeviation{
		Name: name,
	}
}

// Push a float64 to calculate standard deviation and returns σ and whether the number is over 2σ
// 2σ is at odd of every three weeks
func (sd *StandardDeviation) Push(num float64) (float64, bool) {
	sd.Buckets = append(sd.Buckets, num)
	sd.Sum += num
	counter := len(sd.Buckets)
	sd.Mean = sd.Sum / float64(counter)

	var std float64
	for _, v := range sd.Buckets {
		std += math.Pow(v-sd.Mean, 2)
	}

	std = math.Sqrt(std / float64(counter))
	sd.Std = std

	// 2σ evaluation only applies to 10 more data samples
	return std, math.Abs(num-sd.Mean) < 2*std || counter < 10

}
