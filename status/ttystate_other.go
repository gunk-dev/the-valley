//go:build !linux

package main

// ttyState saves nothing where no PIV signer runs (ttystate_linux.go).
type ttyState struct{}

func saveTerminal() *ttyState { return nil }
func (s *ttyState) restore()  {}
func (s *ttyState) release()  {}
