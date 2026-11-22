package cmd

import (
	"fmt"
	"strings"

	"the-valley/flow/internal/git"
	"the-valley/flow/internal/project"
	"the-valley/flow/internal/run"
	"github.com/spf13/cobra"
)

var targetCmd = &cobra.Command{
	Use:   "target [name | owner/repo]",
	Short: "Set or show the session-level default target repo",
	Long: `Sets a session-level default repo so that 'valley flow launch' without --repo
uses this target. Useful when the coordinator session is not inside a git repo.

  valley flow target owner/repo   Set the default target repo
  valley flow target my-project   Set target using a registered project name
  valley flow target              Show the current target repo
  valley flow target --clear      Remove the default target repo`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := sessionStore()
		if err != nil {
			return err
		}
		hds, ok := store.(*run.HomeDirStore)
		if !ok {
			return fmt.Errorf("target command requires a home-dir session store")
		}
		baseDir := hds.BaseDir()

		clear, _ := cmd.Flags().GetBool("clear")

		if clear {
			if err := run.ClearTarget(baseDir); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Target cleared.")
			return nil
		}

		if len(args) == 0 {
			// Show current target
			repo, err := run.LoadTarget(baseDir)
			if err != nil {
				return err
			}
			if repo == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "No target repo set.")
				fmt.Fprintln(cmd.OutOrStdout(), "Usage: valley flow target owner/repo")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), repo)
			}
			return nil
		}

		repoRef := args[0]

		// If no "/" in the reference, try resolving as a registered project name
		if !strings.Contains(repoRef, "/") {
			reg, err := registryUnlessConflict()
			if err != nil {
				return err
			}
			if reg != nil {
				if _, ok := reg.Get(repoRef); ok {
					// It's a known project name — use it as the canonical name
					if err := run.SaveTarget(baseDir, repoRef); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Target set to %s\n", repoRef)
					return nil
				}
			}
		}

		// Validate the repo reference as owner/repo or URL
		owner, repo, _, err := git.ParseRepoRef(repoRef)
		if err != nil {
			return fmt.Errorf("invalid repo reference: %w", err)
		}

		// Normalize: if the repo portion matches a registered project, use the short name
		reg, err := registryUnlessConflict()
		if err != nil {
			return err
		}
		normalized := project.NormalizeRepoName(owner+"/"+repo, reg)

		if err := run.SaveTarget(baseDir, normalized); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Target set to %s\n", normalized)
		return nil
	},
}

func init() {
	targetCmd.Flags().Bool("clear", false, "Remove the default target repo")
	rootCmd.AddCommand(targetCmd)
}
