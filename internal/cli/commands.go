package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"mihatch/internal/app"
	"mihatch/internal/exit"
	"mihatch/internal/paths"
)

// buildApp resolves the project root and constructs an App wired to the
// command's stdio.
func buildApp(cmd *cobra.Command) (*app.App, error) {
	rootFlag, _ := cmd.Flags().GetString("root")
	root, err := paths.ResolveRoot(rootFlag)
	if err != nil {
		return nil, err
	}
	a, err := app.Default(root, cmd.OutOrStdout())
	if err != nil {
		return nil, err
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	a.Verbose = verbose
	a.Err = cmd.ErrOrStderr()
	return a, nil
}

// runCmd builds the app and runs fn, translating errors to stable exit codes.
func runCmd(cmd *cobra.Command, fn func(ctx context.Context, a *app.App) error) error {
	a, err := buildApp(cmd)
	if err != nil {
		return exit.New(exit.CodeConfig, err)
	}
	return fn(cmd.Context(), a)
}

func newInitCommand() *cobra.Command {
	var fromPath, configPath string
	c := &cobra.Command{
		Use:   "init",
		Short: "Install Mihomo, import and purify the source config, and initialize .mihatch",
		Long: `Initialize MiHatch in the current project directory.

By default the source config is imported from the Clash Verge Rev production
runtime (the .dev build is never used). Use --config to import a local Mihomo
YAML instead, and --from to install an offline Mihomo binary.

Init does not start Mihomo and does not touch the system proxy.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error {
				return a.Init(ctx, app.InitOpts{FromPath: fromPath, ConfigPath: configPath})
			})
		},
	}
	c.Flags().StringVar(&fromPath, "from", "", "install Mihomo from a local binary path (offline)")
	c.Flags().StringVar(&configPath, "config", "", "import source config from a local YAML file instead of Clash Verge Rev")
	return c
}

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Re-import the source config and atomically refresh .mihatch/config.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error { return a.Sync(ctx) })
		},
	}
}

func newUpCommand() *cobra.Command {
	var services []string
	var force bool
	c := &cobra.Command{
		Use:   "up",
		Short: "Start Mihomo and take over the macOS system proxy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error {
				return a.Up(ctx, app.UpOpts{Services: services, ForceAuth: force})
			})
		},
	}
	c.Flags().StringSliceVar(&services, "service", nil, "target network service(s) (default: auto from default route)")
	c.Flags().BoolVar(&force, "force", false, "proceed even if the current proxy is authenticated (MiHatch cannot restore its credentials)")
	return c
}

func newStatusCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show MiHatch lifecycle state and liveness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error {
				rep, err := a.Status(ctx)
				if err != nil {
					return exit.New(exit.CodeGeneral, err)
				}
				jsonOut, _ := cmd.Flags().GetBool("json")
				if jsonOut {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(rep)
				}
				renderStatus(cmd, rep)
				return nil
			})
		},
	}
	return c
}

func renderStatus(cmd *cobra.Command, rep app.StatusReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "state:        %s\n", rep.State)
	fmt.Fprintf(out, "initialized:  %t\n", rep.Initialized)
	if rep.EngineVersion != "" {
		fmt.Fprintf(out, "engine:       %s\n", rep.EngineVersion)
	}
	if rep.MixedPort != 0 {
		fmt.Fprintf(out, "mixed-port:   %d\n", rep.MixedPort)
	}
	if rep.PID != 0 {
		fmt.Fprintf(out, "pid:          %d\n", rep.PID)
	}
	fmt.Fprintf(out, "port-listening: %t\n", rep.PortListening)
	fmt.Fprintf(out, "proxy-ok:     %t\n", rep.ProxyOK)
	fmt.Fprintf(out, "proxy-owned:  %t\n", rep.Ownership.Owned)
	if rep.Ownership.Owned {
		fmt.Fprintf(out, "proxy-drifted: %t\n", rep.Ownership.Drifted)
	}
}

func newPauseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Release the system proxy but keep Mihomo running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error { return a.Pause(ctx) })
		},
	}
}

func newResumeCommand() *cobra.Command {
	var services []string
	var force bool
	c := &cobra.Command{
		Use:   "resume",
		Short: "Re-acquire the system proxy with a fresh snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error {
				return a.Resume(ctx, app.UpOpts{Services: services, ForceAuth: force})
			})
		},
	}
	c.Flags().StringSliceVar(&services, "service", nil, "target network service(s) (default: auto from default route)")
	c.Flags().BoolVar(&force, "force", false, "proceed even if the current proxy is authenticated")
	return c
}

func newDownCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Restore the system proxy (if owned) and stop Mihomo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error { return a.Down(ctx) })
		},
	}
}

func newLogsCommand() *cobra.Command {
	var follow bool
	var tail int
	c := &cobra.Command{
		Use:   "logs",
		Short: "Print Mihomo logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd(cmd, func(ctx context.Context, a *app.App) error {
				return a.Logs(ctx, follow, tail)
			})
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	c.Flags().IntVar(&tail, "tail", 50, "number of lines to print from the end (0 = all)")
	return c
}
