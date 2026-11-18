//go:build unix

package ooxmltest

import (
	"os/exec"
	"syscall"
)

// maxOutputBlocks bounds the size of any file an oracle program may write,
// in the blocks of the shell's ulimit. POSIX makes a block 512 bytes, which
// gives 256 MiB; some shells count in 1024 bytes, which gives 512 MiB.
// Either way a converter that loops on a hostile input is stopped by the
// kernel when it reaches the limit.
const maxOutputBlocks = "524288"

// maxOutputBytes is the most a contained program can write to one file,
// whichever block size the shell uses.
const maxOutputBytes = 524288 * 1024

// contain makes sure a program run as an oracle cannot outlive its time
// limit or fill the disk, whatever input it is given.
//
// The program is started in a process group of its own, and the whole group
// is killed when the time is up and again when the call returns.
// LibreOffice's launcher starts the real program as a child; killing only
// the launcher, which is what a context does by default, leaves that child
// running, and on some corpus files it then writes temporary files without
// end. That happened: two files of the corpus made it write 180 GB.
//
// The program is also run under a file-size limit, so that even inside its
// time limit it cannot write a file larger than maxOutputBytes.
func contain(cmd *exec.Cmd) (cleanup func()) {
	args := append([]string{"-c", `ulimit -f ` + maxOutputBlocks + `; exec "$@"`, "sh", cmd.Path}, cmd.Args[1:]...)
	cmd.Path, cmd.Args = "/bin/sh", append([]string{"sh"}, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	killGroup := func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.Cancel = killGroup
	return func() { killGroup() }
}
