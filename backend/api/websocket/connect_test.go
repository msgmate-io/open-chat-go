package websocket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/coder/websocket"
)

func TestIsExpectedSocketClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: true},
		{name: "context canceled", err: context.Canceled, want: true},
		{name: "wrapped context canceled", err: fmt.Errorf("write: %w", context.Canceled), want: true},
		{name: "net closed", err: net.ErrClosed, want: true},
		{name: "normal closure", err: websocket.CloseError{Code: websocket.StatusNormalClosure}, want: true},
		{name: "going away", err: websocket.CloseError{Code: websocket.StatusGoingAway}, want: true},
		{name: "abnormal closure", err: websocket.CloseError{Code: websocket.StatusAbnormalClosure}, want: true},
		{name: "policy violation", err: websocket.CloseError{Code: websocket.StatusPolicyViolation}, want: false},
		{name: "generic error", err: errors.New("boom"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExpectedSocketClose(tt.err); got != tt.want {
				t.Fatalf("isExpectedSocketClose(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
