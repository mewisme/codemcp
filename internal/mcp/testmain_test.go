package mcp

import (
	"fmt"
	"os"
	"testing"

	"go.mewis.me/codemcp/internal/testutil"
)

func TestMain(m *testing.M) {
	_, cleanup, err := testutil.IsolateConfigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
