//go:build unix

package ooxmltest

import (
	"os/exec"
	"syscall"
)

// maxOutputBlocks bounds the size of any file an oracle program may write,
// in the 512-byte blocks of the shell's ulimit: 256 MiB. A converter that
// loops on a hostile input is stopped by the kernel when it reaches it.
const maxOutputBlocks = "524288"

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
// time limit it cannot write a file larger than maxOutputBlocks.
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
