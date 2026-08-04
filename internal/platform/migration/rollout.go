package migrationkit

import "fmt"

func ValidatePercentageBasisPoints(value int) error {
	if value < 0 || value > BasisPoints {
		return fmt.Errorf("%w: %d", ErrInvalidPercentage, value)
	}
	return nil
}

func SuggestedRamp() []int {
	return []int{10, 100, 500, 1000, 2500, 5000, 10000}
}
