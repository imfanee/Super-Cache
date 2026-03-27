// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Exponential backoff with jitter for outbound dial retries (P2.1).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"context"
	"math/rand/v2"
	"time"
)

const (
	backoffInitial = 1 * time.Second
	backoffMax     = 30 * time.Second
)

// backoffSleep waits for the next dial retry delay (initial 1s, cap 30s, ~25% jitter).
func backoffSleep(ctx context.Context, attempt int) error {
	d := backoffInitial
	for i := 0; i < attempt && d < backoffMax; i++ {
		d *= 2
		if d > backoffMax {
			d = backoffMax
			break
		}
	}
	if d > backoffMax {
		d = backoffMax
	}
	jitter := time.Duration(rand.Int64N(int64(d / 4)))
	delay := d + jitter
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}
