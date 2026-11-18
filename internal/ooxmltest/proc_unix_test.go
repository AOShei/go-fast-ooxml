//go:build unix

package ooxmltest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An oracle program must not outlive its time limit or fill the disk. The
// stand-in here behaves like LibreOffice on a hostile file: a launcher
// starts a child, and the child writes without end.
func TestOracleProgramsAreContained(t *testing.T) {
	dir := t.TempDir()
	script := `( while :; do head -c 1048576 /dev/zero >> "$1/big" 2>/dev/null; done ) & echo $! > "$1/pid"; wait`
	start := time.Now()
	_, _, err := runContained(3*time.Second, "sh", "-c", script, "sh", dir)
	if err == nil {
		t.Error("a program that never ends returned without error")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("the call took %v; the time limit was 3s", elapsed)
	}

	// The child of the launcher is gone too, not only the launcher.
	raw, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the launcher's child (pid %d) is still running after the call returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// And it could not write a file past the limit while it ran.
	info, err := os.Stat(filepath.Join(dir, "big"))
	if err != nil {
		t.Fatal(err)
	}
	// What matters is that the file stops growing near the limit. How
	// exactly the limit is enforced differs by system: macOS has let a
	// file pass it by a little under 2 MiB. The slack allows for that and
	// still fails on a file that keeps growing.
	const slack = 16 << 20
	if info.Size() > maxOutputBytes+slack {
		t.Errorf("the program wrote %d bytes; the limit is %d", info.Size(), maxOutputBytes)
	}
	if info.Size() < maxOutputBytes/4 {
		t.Logf("the program wrote %d MiB in its time: the %d MiB limit itself was not reached", info.Size()>>20, maxOutputBytes>>20)
	}
	size := info.Size()
	time.Sleep(300 * time.Millisecond)
	if again, _ := os.Stat(filepath.Join(dir, "big")); again.Size() != size {
		t.Error("the file is still growing after the call returned")
	}
}

func TestTidyRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "profile", "user"), 0o755)
	for _, name := range []string{"lu123abc.tmp", ".~lock.input.txt#", "input.txt", "done"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	tidy(dir)
	left, _ := os.ReadDir(dir)
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "done input.txt" {
		t.Errorf("left behind: %v", names)
	}
}
