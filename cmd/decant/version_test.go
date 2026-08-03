package main

import (
	"strings"
	"testing"
)

func TestBuildVersionPrefersTheLinkerValue(t *testing.T) {
	// A release build stamps an exact string and nothing should override it.
	old := version
	defer func() { version = old }()

	version = "v2.3.4"
	if got := buildVersion(); got != "v2.3.4" {
		t.Errorf("buildVersion() = %q, want the linker value", got)
	}
}

func TestBuildVersionFallsBackToBuildInfo(t *testing.T) {
	// With no linker value, the toolchain's record is used. This is the case
	// that matters: "go install ...@v1.0.0" reports v1.0.0 rather than a
	// placeholder, which it did not before.
	old := version
	defer func() { version = old }()

	version = ""
	got := buildVersion()
	if got == "" {
		t.Fatal("buildVersion() is empty")
	}
	if got == "dev" {
		t.Error("buildVersion() still reports the old placeholder")
	}
	// Under "go test" the binary is built from the module, so this should be
	// either a module version or a VCS-derived string, never "unknown".
	if got == "unknown" {
		t.Errorf("buildVersion() = %q; build info was expected to be present", got)
	}
}

func TestVersionCommandReportsIt(t *testing.T) {
	code, stdout, _ := runCLI("version")
	if code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.HasPrefix(stdout, "decant ") {
		t.Errorf("version output = %q", stdout)
	}
	if strings.Contains(stdout, "decant dev ") {
		t.Errorf("version still reports the placeholder: %q", stdout)
	}
}
