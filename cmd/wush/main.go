package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/pretty"
	"github.com/coder/serpent"
	"github.com/coder/wush/cliui"
	"github.com/mitchellh/go-wordwrap"
)

func main() {
	var (
		showVersion bool

		fmtLong = "wush %s - peer-to-peer file transfers and shells\n"
	)
	cmd := &serpent.Command{
		Use: "wush <subcommand>",
		Long: fmt.Sprintf(fmtLong, getBuildInfo().version) + formatExamples(
			example{
				Description: "Start the wush server",
				Command:     "wush receive",
			},
			example{
				Description: "Open a shell to the wush host",
				Command:     "wush ssh",
			},
			example{
				Description: "Transfer files to the wush host using rsync",
				Command:     "wush rsync local-file.txt :/path/to/remote/file",
			},
		),
		Handler: func(i *serpent.Invocation) error {
			if showVersion {
				return versionCmd().Handler(i)
			}
			return serpent.DefaultHelpFn()(i)
		},
		Children: []*serpent.Command{
			versionCmd(),
			sshCmd(),
			serveCmd(),
			rsyncCmd(),
			cpCmd(),
		},
		Options: []serpent.Option{
			{
				Flag:        "version",
				Description: "Print the version and exit.",
				Value:       serpent.BoolOf(&showVersion),
			},
		},
	}

	err := cmd.Invoke().WithOS().Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// example represents a standard example for command usage, to be used
// with formatExamples.
type example struct {
	Description string
	Command     string
}

// formatExamples formats the examples as width wrapped bulletpoint
// descriptions with the command underneath.
func formatExamples(examples ...example) string {
	var sb strings.Builder

	padStyle := cliui.DefaultStyles.Wrap.With(pretty.XPad(4, 0))
	for i, e := range examples {
		if len(e.Description) > 0 {
			wordwrap.WrapString(e.Description, 80)
			_, _ = sb.WriteString(
				"  - " + pretty.Sprint(padStyle, e.Description+":")[4:] + "\n\n    ",
			)
		}
		// We add 1 space here because `cliui.DefaultStyles.Code` adds an extra
		// space. This makes the code block align at an even 2 or 6
		// spaces for symmetry.
		_, _ = sb.WriteString(" " + pretty.Sprint(cliui.DefaultStyles.Code, fmt.Sprintf("$ %s", e.Command)))
		if i < len(examples)-1 {
			_, _ = sb.WriteString("\n\n")
		}
	}
	return sb.String()
}

var (
	version    string
	commit     string
	commitDate string
)

type buildInfo struct {
	version    string
	commitHash string
	commitTime time.Time
}

func getBuildInfo() buildInfo {
	bi := buildInfo{
		version:    "v0.0.0-devel",
		commitHash: "0000000000000000000000000000000000000000",
		commitTime: time.Now(),
	}

	if version != "" {
		bi.version = version
	}
	if commit != "" {
		bi.commitHash = commit
	}
	if commitDate != "" {
		dateUnix, err := strconv.ParseInt(commitDate, 10, 64)
		if err != nil {
			panic("invalid commitDate: " + err.Error())
		}
		bi.commitTime = time.Unix(dateUnix, 0)
	}

	return bi
}
