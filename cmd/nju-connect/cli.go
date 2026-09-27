package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/soundadam/nju-connect/internal/app"
)

// exitCode is an error that only carries an exit status: whatever the user
// needs to see has already been written.
type exitCode int

func (code exitCode) Error() string { return fmt.Sprintf("exit status %d", int(code)) }

// helpfulUsage is a usage error that also shows the command's help on
// stderr: an unknown command or flag.
type helpfulUsage struct {
	err     error
	command *cobra.Command
}

func withHelp(command *cobra.Command, err error) error {
	return &helpfulUsage{err: err, command: command}
}

func (usage *helpfulUsage) Error() string { return usage.err.Error() }

func (usage *helpfulUsage) Unwrap() error { return usage.err }

// exitStatus is the single mapping from a command's error to its exit status
// and stderr message: nil → 0, cancellation → 0, app.UsageError → 2,
// anything else → 1.
func exitStatus(err error, stderr io.Writer) int {
	var code exitCode
	var usage *helpfulUsage
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	case errors.Is(err, context.Canceled):
		return 0
	case errors.As(err, &usage):
		fmt.Fprintln(stderr, err)
		writeHelp(stderr, usage.command)
		return 2
	case app.IsUsage(err):
		fmt.Fprintln(stderr, err)
		return 2
	default:
		fmt.Fprintln(stderr, err)
		return 1
	}
}

// writeHelp prints a command's hand-written usage, or its flags.
func writeHelp(output io.Writer, command *cobra.Command) {
	if command.Long != "" {
		fmt.Fprintln(output, command.Long)
		return
	}
	fmt.Fprintf(output, "Usage of %s:\n%s", command.CommandPath(), command.LocalFlags().FlagUsages())
}

// flagError turns a flag parse error into a usage error. pflag reads the
// single-dash "-json" as the shorthand cluster -j -s -o -n, so that case
// is reworded to name the flag and the two-dash spelling.
func flagError(command *cobra.Command, err error) error {
	message := err.Error()
	if cut := strings.LastIndex(message, " in -"); strings.HasPrefix(message, "unknown shorthand flag: ") && cut >= 0 {
		spelled := message[cut+len(" in -"):]
		if name, _, _ := strings.Cut(spelled, "="); len(name) > 1 {
			message = "unknown flag: -" + spelled
			if command.Flags().Lookup(name) != nil {
				message += fmt.Sprintf(" (long flags take two dashes: --%s)", name)
			}
		}
	}
	return withHelp(command, app.Usagef("%s", message))
}

// finishCommands applies the conventions every command shares: a hidden
// -h/--help flag, and no positional arguments unless the command says so.
func finishCommands(command *cobra.Command) {
	command.InitDefaultHelpFlag()
	_ = command.Flags().MarkHidden("help")
	if command.Args == nil {
		command.Args = noArgs
	}
	for _, child := range command.Commands() {
		finishCommands(child)
	}
}

func noArgs(command *cobra.Command, arguments []string) error {
	if len(arguments) > 0 {
		return app.Usagef("%s accepts no positional arguments", commandName(command))
	}
	return nil
}

// commandName is the command path without the program name, as usage
// errors spell it ("speedtest component status").
func commandName(command *cobra.Command) string {
	return strings.TrimPrefix(command.CommandPath(), command.Root().Name()+" ")
}
