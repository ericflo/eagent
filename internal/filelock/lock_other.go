//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package filelock

import (
	"context"
	"fmt"
)

func Acquire(context.Context, string) (func(), error) {
	return nil, fmt.Errorf("settings writes require a platform with advisory file locks")
}
