package broker

import (
	"context"
	"net"
	"net/http"
	"time"
)

func Client(socket string) *http.Client {
	return ClientWithHeaderTimeout(socket, 20*time.Second)
}

func ClientWithHeaderTimeout(socket string, timeout time.Duration) *http.Client {
	return &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: timeout, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, MaxIdleConns: 4, MaxIdleConnsPerHost: 4}}
}
