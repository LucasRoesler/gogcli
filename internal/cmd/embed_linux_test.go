package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// openPTY returns the follower end of a new pseudo-terminal, so a test can
// give the process a terminal stdin as a TUI does.
func openPTY(t *testing.T) *os.File {
	t.Helper()

	leader, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}

	t.Cleanup(func() { _ = leader.Close() })

	fd := int(leader.Fd())
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pty: %v", err)
	}

	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pty number: %v", err)
	}

	follower, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pty follower: %v", err)
	}

	t.Cleanup(func() { _ = follower.Close() })

	return follower
}

func TestEmbeddedRunsNeverPromptForTheKeyringPasswordOnATerminal(t *testing.T) {
	stdin := os.Stdin
	os.Stdin = openPTY(t)

	t.Cleanup(func() { os.Stdin = stdin })

	if !newDefaultRuntime().KeyringOptions.IsTTY {
		t.Fatal("the CLI runtime should see the pty as a terminal; the test setup is broken")
	}

	if embedRuntime(strings.NewReader(""), io.Discard, io.Discard).KeyringOptions.IsTTY {
		t.Fatal("an embedded run may prompt for the keyring password on the host's terminal")
	}
}
