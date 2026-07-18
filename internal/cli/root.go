package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"mihatch/internal/exit"
)

// Version is the MiHatch CLI version. It may be overridden at build time with
//
//	-ldflags "-X mihatch/internal/cli.Version=vX.Y.Z"
var Version = "0.1.0-dev"

// Globals carries the process-wide flags resolved from the root command.
type Globals struct {
	JSON    bool
	Verbose bool
}

// Root holds shared dependencies and resolved global flags for a command run.
type Root struct {
	Globals
	Stdout io.Writer
	Stderr io.Writer
}

// NewRootCommand builds the cobra root command tree. Subcommands are attached
// incrementally; this keeps the binary buildable at every stage.
func NewRootCommand() (*cobra.Command, *Root) {
	root := &Root{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	cmd := &cobra.Command{
		Use:   "mihatch",
		Short: "A Mihomo escape hatch for macOS network-client development.",
		Long: `MiHatch runs an isolated, user-level Mihomo proxy on macOS so that Clash
Verge Rev production can be fully exited during development of the dev build,
without losing outbound network access.

MiHatch uses only a loopback mixed proxy (no TUN, no DNS hijacking, no root
service) and is fully isolated from Clash Verge Rev's ports, sockets, service
label, and data directory.

Run "mihatch <command> --help" for command-specific usage.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(c *cobra.Command, args []string) error {
			jsonOut, _ := c.Flags().GetBool("json")
			verbose, _ := c.Flags().GetBool("verbose")
			root.Globals = Globals{JSON: jsonOut, Verbose: verbose}
			return nil
		},
	}
	cmd.PersistentFlags().Bool("json", false, "output machine-readable JSON where supported")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "enable verbose logging")
	cmd.PersistentFlags().String("root", "", "project root containing .mihatch/ (default: current directory or MIHATCH_ROOT)")

	cmd.AddCommand(newVersionCommand(root))
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newSyncCommand())
	cmd.AddCommand(newUpCommand())
	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newPauseCommand())
	cmd.AddCommand(newResumeCommand())
	cmd.AddCommand(newDownCommand())
	cmd.AddCommand(newLogsCommand())
	return cmd, root
}

// Execute parses argv and runs the selected command. It installs a signal
// handler so Ctrl-C produces a clean, non-zero exit instead of a stack trace.
func Execute() int {
	cmd, _ := NewRootCommand()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "mihatch:", err)
		return exit.Code(err)
	}
	return exit.CodeOK
}

func newVersionCommand(root *Root) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the MiHatch version",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := root.Stdout
			if root.Globals.JSON {
				fmt.Fprintf(out, `{"name":"mihatch","version":%q}`+"\n", Version)
			} else {
				fmt.Fprintf(out, "mihatch version %s\n", Version)
			}
			return nil
		},
	}
}
