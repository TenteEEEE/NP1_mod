package adapters

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func lockUplinkWiFi(ctx context.Context) (func(), error) {
	f, err := os.OpenFile(uplinkPrivate+"/wifi.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for ctx.Err() == nil {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	_ = f.Close()
	return nil, ctx.Err()
}
