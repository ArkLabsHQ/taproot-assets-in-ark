package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	// Create a new reader from standard input
	reader := bufio.NewReader(os.Stdin)
	// Define a command-line flag called "network" to determine which network.
	network := flag.String("network", "regtest", "Specify the network to run the application")

	// Parse the command-line flags.
	flag.Parse()

	log.Println("Welcome to the Taproot Assets CLI")
	log.Println("------------------------------------------------")

	log.Println("Network: ", *network)

	// Initialise the App
	app := Init(*network)

	for {
		// Display prompt
		fmt.Print(">> ")
		// Read input until newline
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading input:", err)
			continue
		}

		// Trim newline and whitespace
		input = strings.TrimSpace(input)

		// Check for exit commands
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		// Process the command
		processInput(input, &app)
	}

}

func processInput(input string, app *App) {
	switch input {
	case "board":
		app.Board()
	case "round":
		app.ConstructRound()
	case "unilateral":
		app.ExitRound()
	case "tree":
		app.ShowRoundTree()
	case "balance":
		app.ShowBalance()
	case "mint":
		app.Mint()
	case "deposit":
		app.FundOnboarding()
	case "upload":
		app.UploadTokenVtxoProof()

	default:
		log.Println("unknown command")
		log.Println("------------------------------------------------")
	}

}
