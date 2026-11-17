//go:build !linux

package adapters

import (
	"context"
	"errors"
)

func lockUplinkWiFi(context.Context) (func(), error) {
	return nil, errors.New("UPLINK_LOCK_UNAVAILABLE")
}
