package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	// Create a new reader from standard input
	reader := bufio.NewReader(os.Stdin)

	// Initialise the App
	app := Init()

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
		log.Println("Boarding User Complete")
		log.Println("------------------------------------------------")
	case "round":
		app.ConstructRound()
		log.Println("Round Construction Complete")
		log.Println("------------------------------------------------")
	case "upload":
		app.UploadProofs()
		log.Println("Exit Transactions Broadcasted and Proofs Uploaded")
		log.Println("------------------------------------------------")
	case "vtxos":
		app.ShowVtxos()
	case "balance":
		app.ShowBalance()

	default:
		log.Println("unknown command")
		log.Println("------------------------------------------------")
	}

}
