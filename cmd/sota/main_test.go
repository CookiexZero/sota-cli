package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRoutingSelectionBeforeCredentials(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run"}, "--domain or explicit --all"},
		{[]string{"run", "--all", "--domain", "example.com"}, "choose --all or --domain"},
		{[]string{"run", "--all", "--mode", "invalid"}, "mode must be proxy or tun"},
	} {
		err := execute(context.Background(), tc.args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}
