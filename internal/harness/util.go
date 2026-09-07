package harness

import (
	"context"
	"time"
)

func contextWithTimeout(d time.Duration) (context.Context, func()) {
	return context.WithTimeout(context.Background(), d)
}
