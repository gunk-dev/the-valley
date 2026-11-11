package main

import (
	"embed"
	"fmt"
	"io"
	"strings"
)

// The help text, one file per part under help/. usage.txt is what
// `valley help` prints; each public verb's topic is its own file and the
// parts it shares with other topics: naming.txt, how a change is named,
// requests.txt, what unready and withdraw do to a pending request, and
// runtime.txt, what the command needs.
//
//go:embed help/*.txt
var helpFiles embed.FS

// helpTopics is each public verb's help topic, as the parts it prints in
// order. The public verbs are exactly its keys: each is in usage.txt and
// has a topic, and only they write a command record (command.go).
var helpTopics = map[string][]string{
	"pending":     {"pending", "runtime"},
	"changes":     {"changes", "runtime"},
	"review":      {"review", "naming", "runtime"},
	"approve":     {"approve", "naming", "runtime"},
	"rebase":      {"rebase", "naming", "runtime"},
	"status":      {"status", "naming", "runtime"},
	"ready":       {"ready", "naming", "runtime"},
	"unready":     {"unready", "requests", "naming", "runtime"},
	"withdraw":    {"withdraw", "requests", "naming", "runtime"},
	"knot":        {"knot", "runtime"},
	"plan":        {"plan", "runtime"},
	"checks":      {"checks", "runtime"},
	"tail":        {"tail", "runtime"},
	"replay":      {"replay", "runtime"},
	"self-update": {"self-update", "runtime"},
	"telemetry":   {"telemetry"},
}

// publicVerb reports whether verb is one of valley's public verbs.
func publicVerb(verb string) bool {
	_, ok := helpTopics[verb]
	return ok
}

// helpPart is the text of the help file name.txt.
func helpPart(name string) string {
	data, err := helpFiles.ReadFile("help/" + name + ".txt")
	if err != nil {
		panic(err) // every part is embedded, so this is a build that lost one
	}
	return string(data)
}

// helpText is the help topic verb prints, or the usage for "": its parts
// with their trailing blank lines dropped and one newline after them.
func helpText(verb string) string {
	parts := []string{"usage"}
	if verb != "" {
		parts = helpTopics[verb]
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(helpPart(p))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// runHelp is valley help [<verb>]. help, -h and --help name the usage too.
// What it prints goes through droppable, so a reader that stops early,
// as in `valley help review | head -n 1`, leaves the exit status as it was.
func runHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(droppable(stderr), "valley: usage: valley help [<verb>]")
		return 1
	}
	topic := ""
	if len(args) == 1 {
		topic = args[0]
	}
	switch {
	case topic == "help" || topic == "-h" || topic == "--help":
		topic = ""
	case topic != "" && !publicVerb(topic):
		fmt.Fprintf(droppable(stderr), "valley: unknown help topic: %s (try 'valley help')\n", topic)
		return 1
	}
	io.WriteString(droppable(stdout), helpText(topic))
	return 0
}
