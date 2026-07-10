package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Keep one CLI smoke test for command routing and one validation test. The flag
// package and every spelling of invalid input do not need separate coverage.
func TestConversationsExportHelp(t *testing.T) {
	output, err := runCLI(t, "conversations", "export", "-h")
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, output)
	}
	for _, want := range []string{"-channel", "-since", "-to", "-limit", "-no-replies", "-output"} {
		if !strings.Contains(output, want) {
			t.Errorf("export help missing %q:\n%s", want, output)
		}
	}
}

func TestConversationsExportRequiresChannel(t *testing.T) {
	output, err := runCLI(t, "conversations", "export")
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("exit error = %v, want status 2\n%s", err, output)
	}
	if !strings.Contains(output, "-channel is required") {
		t.Errorf("missing required-flag diagnostic:\n%s", output)
	}
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	commandArgs := append([]string{"-test.run=TestCLIHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], commandArgs...)
	cmd.Env = append(os.Environ(), "SLACK_UTILS_CLI_HELPER=1")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("SLACK_UTILS_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			Run(os.Args[i+1:])
			return
		}
	}
	os.Exit(2)
}
