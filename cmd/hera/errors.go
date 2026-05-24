package main

import "fmt"

func errNotYetImplemented(verb string) error {
	return fmt.Errorf("%s is not yet implemented in this build; see openspec/changes/hera-v1/tasks.md for the implementation plan", verb)
}
