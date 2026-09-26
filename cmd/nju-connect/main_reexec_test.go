//go:build linux || darwin

package main

import (
	"os"
	"testing"
)

// cliReexecEnv marks a child started by app.StartBackground from
// inside this test binary. os.Executable() is the test binary there, so the
// child must dispatch the hidden runtime command instead of running tests.
const cliReexecEnv = "NJU_CONNECT_CLI_TEST_REEXEC"

func TestMain(m *testing.M) {
	if os.Getenv(cliReexecEnv) == "1" && len(os.Args) > 1 && os.Args[1] == "_native-runtime" {
		os.Exit(run(productionDeps(), os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
