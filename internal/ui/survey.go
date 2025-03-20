package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/briceamen/logaround/internal/timestamp"
)

// RunSurvey runs an interactive survey to collect user input
func RunSurvey() (string, string, int, int, error) {
	var appName string
	var timestampInput string
	var lineCount int
	var hoursCount int

	reader := bufio.NewReader(os.Stdin)

	// Ask for app name
	fmt.Print("What's the app name? ")
	appName, err := reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("read app name: %w", err)
	}
	appName = strings.TrimSpace(appName)

	// Ask for timestamp
	fmt.Println("\nAround what time should we search? Please use one of the following formats:")
	fmt.Println("  - YYYY-MM-DD HH:MM:SS (e.g., 2023-06-15 14:30:00)")
	fmt.Println("  - Today at HH:MM:SS (e.g., Today at 14:30:00)")
	fmt.Println("  - Yesterday at HH:MM:SS (e.g., Yesterday at 14:30:00)")
	fmt.Println("  - Monday/Tuesday/etc. at HH:MM:SS (e.g., Monday at 14:30:00)")
	fmt.Print("Timestamp: ")
	timestampInput, err = reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("read timestamp: %w", err)
	}

	// Validate and normalize the timestamp
	normalizedTimestamp, err := timestamp.ValidateAndNormalize(strings.TrimSpace(timestampInput))
	if err != nil {
		return "", "", 0, 0, err
	}

	// Ask how they want to filter - by lines or hours
	fmt.Println("\nDo you want to filter by lines or hours around the timestamp?")
	fmt.Println("  1. Lines (default)")
	fmt.Println("  2. Hours")
	fmt.Print("Enter your choice (1 or 2): ")
	choiceStr, err := reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("read filter choice: %w", err)
	}

	choice := strings.TrimSpace(choiceStr)

	if choice == "2" {
		// Ask for hours count
		fmt.Print("\nHow many hours do you want before and after the timestamp? ")
		_, err = fmt.Scanf("%d\n", &hoursCount)
		if err != nil {
			// Try reading as string and convert
			hoursStr, err := reader.ReadString('\n')
			if err != nil {
				return "", "", 0, 0, fmt.Errorf("read hours count: %w", err)
			}
			_, err = fmt.Sscanf(hoursStr, "%d", &hoursCount)
			if err != nil {
				return "", "", 0, 0, fmt.Errorf("parse hours count: %w", err)
			}
		}

		// Default to 1 hour if no valid input
		if hoursCount <= 0 {
			hoursCount = 1
			fmt.Println("Using default hours count: 1 hour before and after")
		}
	} else {
		// Ask for line count
		fmt.Print("\nHow many lines do you want before and after the timestamp? ")
		_, err = fmt.Scanf("%d\n", &lineCount)
		if err != nil {
			// Try reading as string and convert
			lineStr, err := reader.ReadString('\n')
			if err != nil {
				return "", "", 0, 0, fmt.Errorf("read line count: %w", err)
			}
			_, err = fmt.Sscanf(lineStr, "%d", &lineCount)
			if err != nil {
				return "", "", 0, 0, fmt.Errorf("parse line count: %w", err)
			}
		}

		// Default to 100 if no valid input
		if lineCount <= 0 {
			lineCount = 100
			fmt.Println("Using default line count: 100 lines before and after")
		}
	}

	return appName, normalizedTimestamp, lineCount, hoursCount, nil
}
