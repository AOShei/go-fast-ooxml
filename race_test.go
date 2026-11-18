//go:build race

package gofastooxml_test

// underRace says the tests were built with the race detector, which makes
// a program hold several times the memory it otherwise would.
const underRace = true
