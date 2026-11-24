//go:build !xmlscanpoison

package xmlscan

// poison is set by the xmlscanpoison tag: see poison.go.
const poison = false

// Poisoned reports whether the xmlscanpoison tag is set.
const Poisoned = poison

func (s *Scanner) poisonViews()    {}
func (s *Scanner) poisonReleased() {}
