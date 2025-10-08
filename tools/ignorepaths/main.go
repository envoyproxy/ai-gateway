// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/monochromegane/go-gitignore"
)

// Takes a list of file paths from stdin and filters them based on the given .gitignore-style file.

func main() {
	gi, err := gitignore.NewGitIgnore(os.Args[1], ".")
	if err != nil {
		panic(err)
	}

	s := bufio.NewScanner(os.Stdin)
	for s.Scan() {
		if !gi.Match(s.Text(), false) {
			_, _ = fmt.Fprintln(os.Stdout, s.Text())
		}
	}
}
