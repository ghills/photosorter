package ui

import (
	"context"
	"net/http"
)

// NewTestHandler exposes the interface's handler and token to the end-to-end test,
// which lives in package ui_test so it can import cli.
func NewTestHandler(ctx context.Context, opts Options) (http.Handler, string, func()) {
	s, err := newServer(ctx, opts)
	if err != nil {
		panic(err)
	}
	return s.handler(), s.token, s.shutdown
}
