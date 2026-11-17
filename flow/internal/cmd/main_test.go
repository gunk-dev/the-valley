package cmd

import (
	"os"
	"testing"

	"the-valley/flow/internal/tmux/tmuxtest"
)

func TestMain(m *testing.M) { os.Exit(tmuxtest.Main(m)) }
