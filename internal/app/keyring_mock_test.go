package app

import "github.com/zalando/go-keyring"

// Tests, and the runtime children they re-execute, never reach the real
// system keyring.
func init() {
	keyring.MockInit()
}
