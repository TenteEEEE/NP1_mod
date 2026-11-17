package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/np1-control-center/ncc/internal/api"
	"github.com/np1-control-center/ncc/internal/numbershare"
)

func runNumberShareSync(ctx context.Context, stateDir, syncConfig, positionURL string) error {
	// The owner dashboard intentionally occupies the public web ports, so the
	// full ncc server is not required. Keep the plate record collector running
	// beside the offline/retry uploader instead.
	go api.RunPlateCollector(ctx, stateDir, positionURL)
	return numbershare.New(syncConfig, stateDir).Run(ctx)
}

func runNumberShareCheck(ctx context.Context, stateDir, syncConfig string) error {
	result, err := numbershare.New(syncConfig, stateDir).Check(ctx)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		return encodeErr
	}
	return err
}
func runNumberShareRegister(ctx context.Context, options numbershare.RegistrationOptions) error {
	result, err := numbershare.NewRegistrar(options).Ensure(ctx)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		return encodeErr
	}
	return err
}

