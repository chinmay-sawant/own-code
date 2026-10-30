package app

import (
	owncode "github.com/chinmay-sawant/own-code"
)

// file returns one embedded component or stylesheet as text. The template
// set lives at the repository root; see the owncode package.
func file(name string) string {
	b, err := owncode.Components.ReadFile(name)
	if err != nil {
		return ""
	}

	return string(b)
}
