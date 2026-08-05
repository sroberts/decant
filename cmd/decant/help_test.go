package main

import (
	"strings"
	"testing"
)

func TestSubcommandHelpIsSuccess(t *testing.T) {
	verbs := []string{"convert", "probe", "meta", "profile"}
	helpFlags := []string{"-h", "--help"}

	for _, verb := range verbs {
		for _, hf := range helpFlags {
			t.Run(verb+"/"+hf, func(t *testing.T) {
				code, _, stderr := runCLI(verb, hf)
				if code != exitOK {
					t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, exitOK, stderr)
				}
				if !strings.Contains(stderr, "Usage:") {
					t.Errorf("stderr does not show usage:\n%s", stderr)
				}
			})
		}
	}
}
