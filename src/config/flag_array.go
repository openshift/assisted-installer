package config

import (
	"strings"
)

// New type to be used by the built-in go `flag` library -
// this type allows flags to be passed multiple times in the
// commandline arguments, such that all values of flags become
// members of this array
type ArrayFlags []string

// String is implemented to fit the flag.Value interface
func (a *ArrayFlags) String() string {
	return strings.Join([]string(*a), ",")
}

// Set is implemented to fit the flag.Value interface
func (a *ArrayFlags) Set(value string) error {
	// Each time we encounter the flag, add an entry to the list,
	// this way the flag can be specified multiple times
	*a = append(*a, value)
	return nil
}
