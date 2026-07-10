package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestConversationsExportHelp(t *testing.T) {
	output, err := runCLI(t, "conversations", "export", "-h")
	if err != nil {
		t.Fatalf("help command failed: %v\n%s", err, output)
	}

	for _, want := range []string{
		"slack-utils conversations export -channel C123",
		"-channel string",
		"C123..., G123..., or D123...",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output does not contain %q:\n%s", want, output)
		}
	}
}

func TestConversationsExportRequiresChannel(t *testing.T) {
	output, err := runCLI(t, "conversations", "export")
	if err == nil {
		t.Fatalf("command succeeded without -channel:\n%s", output)
	}
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
