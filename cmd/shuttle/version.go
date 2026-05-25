package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "dev"
var Commit = "unknown"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the shuttle version and build commit",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("shuttle %s (%s)\n", Version, Commit)
	},
}
