//go:build !linux || !cgo

package nvmlpmon

import (
	"errors"

	"github.com/brantje/fake-nvidia/internal/pmon"
)

// Collect reports that the pmon compatibility path requires Linux with CGo.
func Collect() ([]pmon.Sample, error) {
	return nil, errors.New("NVML pmon collection requires linux with cgo enabled")
}
