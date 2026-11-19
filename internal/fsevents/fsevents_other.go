//go:build !darwin

package fsevents

import (
	"errors"
	"time"

	"github.com/tavodev/diskpy/internal/scan"
)

var ErrFullRescan = errors.New("fsevents: not supported, full rescan needed")

func Now() uint64 { return 0 }

func Since(string, uint64, time.Duration) ([]scan.Change, error) { return nil, ErrFullRescan }
