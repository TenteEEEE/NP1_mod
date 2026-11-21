//go:build !linux

package update

import (
	"context"
	"errors"
	"io"
)

var errPlatform = errors.New("the NP1 updater requires Linux")

func Serve(context.Context) error                                 { return errPlatform }
func Recover(context.Context) error                               { return errPlatform }
func CheckKey(string) error                                       { return errPlatform }
func Fetch(context.Context, bool, string, int64, io.Writer) error { return errPlatform }
