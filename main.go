package main

import (
	"fmt"
	"log"
	"os"

	// playwright
	"github.com/playwright-community/playwright-go"
)

const (
	messageUsage = `Usage:

	$ %s [CONFIG_FILE_PATH]
`
)

func main() {
	// install playwright browsers
	if err := playwright.Install(); err != nil {
		log.Printf("failed to install playwright browsers: %s", err)
		return
	}

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
