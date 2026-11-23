package cmd

import (
	"fmt"

	"the-valley/flow/internal/config"
	"the-valley/flow/internal/git"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize flow config",
	Long: `Scaffolds default config.json and prompt.md template.

If run inside a git repository, writes them in the repo's .klaus/ (or over the
ones in .valley/flow/, where a repo keeps them there).
If run outside a git repository, writes the host config, ~/.klaus/config.json
(or ~/.valley/flow/config.json, where the host keeps it).
Under the bridge naming generation, a file that doesn't exist yet goes under
.valley/flow/ (~/.valley/flow/ for the host config), and the config's
worktree_base is the bridge default.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := git.RepoRoot()

		if root != "" {
			configPath, promptPath, err := config.Init(root)
			if err != nil {
				return err
			}
			fmt.Println("Initialized flow config:")
			fmt.Printf("  %s  — configuration\n", configPath)
			fmt.Printf("  %s  — system prompt template\n", promptPath)
		} else {
			path, err := config.InitGlobal()
			if err != nil {
				return err
			}
			fmt.Println("Initialized global flow config:")
			fmt.Printf("  %s  — configuration\n", path)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
