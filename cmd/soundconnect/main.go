package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		writeUsage(stdout)
		return 0
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		writeUsage(stdout)
		return 0
	case "version":
		fmt.Fprintf(stdout, "soundconnect %s\n", version)
		return 0
	case "setup", "connect", "status", "doctor", "observe":
		fmt.Fprintf(stderr, "soundconnect %s is not implemented yet\n", arguments[0])
		return 2
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		writeUsage(stderr)
		return 2
	}
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, `usage: soundconnect <command>

commands:
  setup      configure the account and long-lived password
  connect    authenticate interactively and run the connection
  status     print sanitized runtime status
  doctor     inspect the local development environment
  observe    record a sanitized behavior timeline
  version    print build identity`)
}
