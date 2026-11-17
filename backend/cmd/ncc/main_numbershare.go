package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/np1-control-center/ncc/internal/numbershare"
)

func runNumberShareSync(ctx context.Context, stateDir, syncConfig, positionURL string) error {
	// Compatibility mode for installations that still have main's former
	// standalone unit enabled. Import its credential once, then remain idle:
	// the owner dashboard is the sole collector and NumberShare sender.
	if _, err := numbershare.MigrateLegacyConfig(syncConfig, stateDir); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func runNumberShareCheck(ctx context.Context, stateDir string) error {
	identity, err := numbershare.New(stateDir, nil).Check(ctx)
	result := map[string]any{"reachable": err == nil, "authenticated": err == nil, "status": "ok"}
	if err != nil {
		result["status"] = "error"
	} else if handle, ok := identity["user_handle"]; ok {
		result["user_handle"] = handle
	}
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
