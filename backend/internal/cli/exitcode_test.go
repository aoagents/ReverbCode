package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil is success", nil, 0},
		{"runtime error is 1", errors.New("boom"), 1},
		{"usage error is 2", usageError{errors.New("bad flag")}, 2},
		{"wrapped usage error is still 2", fmt.Errorf("ctx: %w", usageError{errors.New("x")}), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
