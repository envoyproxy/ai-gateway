// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package version

import (
	"fmt"
	"strconv"
	"strings"
)

// version is the current version of build. This is populated by the Go linker.
var version string

// Current version with the Git information.
func Current() Git {
	return parseGit(version)
}

// Parse returns the parsed service's version information. (from raw git label)
func Parse() string {
	return Current().String()
}

// Git contains the version information extracted from a Git SHA.
type Git struct {
	ClosestTag   string
	CommitsAhead int
	Sha          string
}

func (g Git) String() string {
	switch {
	case g == Git{}:
		// unofficial version built without using the make tooling
		return "dev"
	case g.CommitsAhead != 0:
		// built from a non release commit point
		// In the version string, the commit tag is prefixed with "-g" (which stands for "git").
		// When printing the version string, remove that prefix to just show the real commit hash.
		return fmt.Sprintf("%s (%s, +%d)", g.Sha, g.ClosestTag, g.CommitsAhead)
	default:
		return g.ClosestTag
	}
}

// parseGit the given version string into a version object. The input version string
// is in the format:
//
//	<release tag>-<commits since release tag>-g<commit hash>
func parseGit(v string) Git {
	// ensure that at least we should be able to parse release tag, commits, hash
	if len(strings.Split(v, "-")) < 3 {
		return Git{}
	}

	// The git tag could contain '-' characters, so we start parting the version string
	// from the last parts, and concatenate the remaining ones at the beginning to reconstruct
	// the original tag if it had '-' characters.
	parts := strings.Split(v, "-")
	l := len(parts)
	commits, err := strconv.Atoi(parts[l-2])
	if err != nil { // extra safety but should never happen
		return Git{}
	}

	return Git{
		ClosestTag:   strings.Join(parts[:l-2], "-"),
		CommitsAhead: commits,
		Sha:          parts[l-1][1:], // remove the 'g' prefix
	}
}
