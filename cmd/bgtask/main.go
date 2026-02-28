package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/alecthomas/kong"
	"github.com/philsphicas/bgtask/internal/state"
	"github.com/willabides/kongplete"
)

// version is set at build time via -ldflags.
var version = "dev"

func init() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		}
	}
}

// CLI defines the top-level command structure for bgtask.
var CLI struct {
	Run     RunCmd     `cmd:"" help:"Launch a background task."`
	Ls      LsCmd      `cmd:"" help:"List tasks."`
	Status  StatusCmd  `cmd:"" help:"Show task details."`
	Logs    LogsCmd    `cmd:"" help:"View task logs."`
	Stop    StopCmd    `cmd:"" help:"Stop a task."`
	Pause   PauseCmd   `cmd:"" help:"Pause a task (supervisor stays alive)."`
	Resume  ResumeCmd  `cmd:"" help:"Resume a paused task."`
	Rename  RenameCmd  `cmd:"" help:"Rename a task."`
	Rm      RmCmd      `cmd:"" help:"Stop and delete a task."`
	Cleanup CleanupCmd `cmd:"" help:"Remove all non-running tasks."`

	Completion kongplete.InstallCompletions `cmd:"" help:"Output shell completion script."`

	// Hidden supervisor subcommand -- used internally for re-exec.
	Supervisor SupervisorCmd `cmd:"" hidden:""`

	Version VersionFlag `name:"version" help:"Print version."`
}

// VersionFlag is a boolean flag that prints the version and exits.
type VersionFlag bool

// BeforeApply prints the version and exits.
func (v VersionFlag) BeforeApply(app *kong.Kong) error {
	fmt.Fprintln(app.Stdout, version)
	app.Exit(0)
	return nil
}

func main() {
	parser := kong.Must(&CLI,
		kong.Name("bgtask"),
		kong.Description("Background tasks you can find again."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.Help(customHelpPrinter),
	)

	kongplete.Complete(parser)

	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)

	// Initialize the store once for all commands (except supervisor, which
	// creates its own from explicit args).
	store, err := state.DefaultStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := ctx.Run(store); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
