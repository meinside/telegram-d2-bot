package main

import (
	"fmt"
	"os"
)

const (
	messageUsage = `Usage:

	$ %s [CONFIG_FILE_PATH]
`
)

func main() {
	if len(os.Args) > 1 {
		runBot(os.Args[1])
	} else {
		printUsage(os.Args[0])
	}
}

// prints usage text to standard out
func printUsage(progName string) {
	fmt.Printf(messageUsage, progName)
}
