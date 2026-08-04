package main

import (
	"strings"
	"testing"
)

func TestSubcommandHelpIsSuccess(t *testing.T) {
	verbs := []string{"convert", "probe", "meta", "profile"}

	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			code, _, stderr := runCLI(verb, "--help")
			if code != exitOK {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, exitOK, stderr)
			}
			if !strings.Contains(stderr, "Usage:") {
				t.Errorf("stderr does not show usage:\n%s", stderr)
			}
		})
	}
}
