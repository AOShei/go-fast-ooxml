//go:build !unix

package ooxmltest

import "os/exec"

// contain is the containment of oracle programs. The oracles (LibreOffice,
// pandoc, the Python tools) are only run on Unix development machines; on
// other systems the tests that use them skip, and nothing is contained.
func contain(cmd *exec.Cmd) (cleanup func()) { return func() {} }
