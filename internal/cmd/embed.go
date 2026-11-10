package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	goruntime "runtime"

	"github.com/openclaw/gogcli/internal/app"
	"github.com/openclaw/gogcli/internal/config"
	"github.com/openclaw/gogcli/internal/secrets"
)

// embedSlot lets one ExecuteIO run at a time: a run swaps the process-wide
// slog default logger (root.go), so concurrent runs would log into each
// other's writers. A channel, unlike a mutex, lets a waiting caller give up
// when its context ends.
var embedSlot = make(chan struct{}, 1)

// ExecuteIO runs one gog command line in-process with the caller's stdin,
// stdout and stderr, so a program in this module (contrib/gog-inbox) shares
// gog's accounts, keyring, config and reauth handling without spawning gog.
// Cancelling ctx stops the wait for an earlier run and the command's Google
// calls.
//
// The caller owns the terminal, so the file keyring never prompts for its
// password: without GOG_KEYRING_PASSWORD the command fails instead.
//
// This file is fork-local and additive: it only wraps
// executeWithRuntimeContext, so upstream syncs touch it only when that
// function's signature changes.
func ExecuteIO(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	select {
	case embedSlot <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("wait for an earlier gog command: %w", ctx.Err())
	}
	defer func() { <-embedSlot }()

	return executeWithRuntimeContext(ctx, args, embedRuntime(in, out, errOut))
}

func embedRuntime(in io.Reader, out, errOut io.Writer) *app.Runtime {
	runtime := newDefaultRuntime()
	runtime.IO.In, runtime.IO.Out, runtime.IO.Err = in, out, errOut

	// newDefaultRuntime allows the password prompt when os.Stdin is a
	// terminal, which it is for a TUI.
	keyring := secrets.OpenOptionsFromLookup(config.Layout{}, nil, os.LookupEnv, goruntime.GOOS, false)
	runtime.KeyringOptions = &keyring

	return runtime
}
