package app

import (
	"os"
	"testing"
)

// captureExit swaps the fatal exit for one that records its message.
func captureExit(t *testing.T) *[]string {
	t.Helper()
	orig := exit
	var msgs []string
	exit = func(msg string, _ int) { msgs = append(msgs, msg) }
	t.Cleanup(func() { exit = orig })
	return &msgs
}

// TestConfigFlagNeedsAValue is the regression test for the bounds check: -c as
// the last argument, with other arguments before it, used to index past the
// end and panic instead of reporting the missing value.
func TestConfigFlagNeedsAValue(t *testing.T) {
	args, cfg := os.Args, CFG.cfg
	defer func() { os.Args, CFG.cfg = args, cfg }()
	msgs := captureExit(t)

	os.Args = []string{"skunkyart", "-x", "-c"}
	ExecuteCommandLineArguments()

	if len(*msgs) != 1 {
		t.Fatalf("exit called %d times, want 1 usage error", len(*msgs))
	}
	if CFG.cfg != cfg {
		t.Errorf("config path changed to %q with no value given", CFG.cfg)
	}
}

func TestConfigFlagTakesTheNextArgument(t *testing.T) {
	args, cfg := os.Args, CFG.cfg
	defer func() { os.Args, CFG.cfg = args, cfg }()
	msgs := captureExit(t)

	os.Args = []string{"skunkyart", "-c", "other.json"}
	ExecuteCommandLineArguments()

	if len(*msgs) != 0 || CFG.cfg != "other.json" {
		t.Errorf("exit calls %v, config path %q; want none and other.json", *msgs, CFG.cfg)
	}
}
