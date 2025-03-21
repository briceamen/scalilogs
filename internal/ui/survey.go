package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/logaround/internal/timestamp"
	"github.com/briceamen/logaround/pkg/scalingo"
)

// RunSurveyFirstPart runs the first part of the interactive survey without needing a client
// It only collects app name, environment and region
func RunSurveyFirstPart(ctx context.Context) (string, string, string, error) {
	var appName string
	var env string
	var region string

	reader := bufio.NewReader(os.Stdin)

	// Ask for app name
	fmt.Print("What's the app name? ")
	appName, err := reader.ReadString('\n')
	if err != nil {
		return "", "", "", errors.Wrap(ctx, err, "read app name")
	}
	appName = strings.TrimSpace(appName)

	// Ask for environment
	fmt.Println("\nWhich environment do you want to use?")
	fmt.Println("  1. Production (default)")
	fmt.Println("  2. Staging")
	fmt.Println("  3. Development")
	fmt.Print("Enter your choice (1-3): ")
	envChoice, err := reader.ReadString('\n')
	if err != nil {
		return "", "", "", errors.Wrap(ctx, err, "read environment choice")
	}

	// Set environment based on choice
	switch strings.TrimSpace(envChoice) {
	case "2":
		env = scalingo.EnvStaging
	case "3":
		env = scalingo.EnvDev
	default:
		env = scalingo.EnvProduction
	}

	// Skip region selection for dev environment
	if env != scalingo.EnvDev {
		// Use predefined regions instead of making API call
		var regions []scalingo.Region

		// Hard-code the regions based on environment to avoid API call
		if env == scalingo.EnvProduction {
			regions = []scalingo.Region{
				{Name: "osc-fr1", DisplayName: "Paris - Outscale"},
				{Name: "osc-secnum-fr1", DisplayName: "Paris - SecNumCloud - Outscale"},
			}
		} else if env == scalingo.EnvStaging {
			regions = []scalingo.Region{
				{Name: "osc-st-fr1", DisplayName: "Paris - Outscale (Staging)"},
			}
		}

		if len(regions) == 0 {
			fmt.Printf("No regions defined for %s. Using default region: %s\n", env, scalingo.RegionOscFr1)
			region = scalingo.RegionOscFr1
		} else {
			fmt.Println("\nPlease select a region:")

			// Create a map of option number to region
			regionOptions := make(map[int]string)
			for i, r := range regions {
				fmt.Printf("  %d. %s (%s)\n", i+1, r.DisplayName, r.Name)
				regionOptions[i+1] = r.Name
			}

			// Ask for region selection
			fmt.Printf("Enter your choice (1-%d): ", len(regions))
			regionChoice, err := reader.ReadString('\n')
			if err != nil {
				return "", "", "", errors.Wrap(ctx, err, "read region choice")
			}

			regionChoiceInt := 0
			fmt.Sscanf(strings.TrimSpace(regionChoice), "%d", &regionChoiceInt)

			// If invalid choice, use first region
			if regionChoiceInt < 1 || regionChoiceInt > len(regions) {
				fmt.Println("Invalid choice. Using first region.")
				region = regions[0].Name
			} else {
				region = regionOptions[regionChoiceInt]
			}
		}
	}

	return appName, env, region, nil
}

// RunSurveySecondPart runs the second part of the interactive survey after a client has been created
// It verifies the app exists and collects timestamp and filter preferences
func RunSurveySecondPart(ctx context.Context, client *scalingo.ScalingoClient, appName string, env string, region string) (string, int, int, error) {
	var timestampInput string
	var lineCount int
	var hoursCount int

	reader := bufio.NewReader(os.Stdin)

	// Check if the app exists in the selected environment and region
	err := client.CheckAppExists(ctx, appName)
	if err != nil {
		fmt.Printf("\nError: The app '%s' does not exist in the %s environment (%s region).\n", appName, env, region)
		return "", 0, 0, errors.Wrap(ctx, err, "find app")
	}

	// Ask for timestamp
	fmt.Println("\nAround what time should we search? Please use one of the following formats:")
	fmt.Println("  - YYYY-MM-DD HH:MM:SS (e.g., 2023-06-15 14:30:00)")
	fmt.Println("  - Today at HH:MM:SS (e.g., Today at 14:30:00)")
	fmt.Println("  - Yesterday at HH:MM:SS (e.g., Yesterday at 14:30:00)")
	fmt.Println("  - Monday/Tuesday/etc. at HH:MM:SS (e.g., Monday at 14:30:00)")
	fmt.Print("Timestamp: ")
	timestampInput, err = reader.ReadString('\n')
	if err != nil {
		return "", 0, 0, errors.Wrap(ctx, err, "read timestamp")
	}

	// Validate and normalize the timestamp
	normalizedTimestamp, err := timestamp.ValidateAndNormalize(ctx, strings.TrimSpace(timestampInput))
	if err != nil {
		return "", 0, 0, err
	}

	// Ask how they want to filter - by lines or hours
	fmt.Println("\nDo you want to filter by lines or hours around the timestamp?")
	fmt.Println("  1. Lines (default)")
	fmt.Println("  2. Hours")
	fmt.Print("Enter your choice (1 or 2): ")
	choiceStr, err := reader.ReadString('\n')
	if err != nil {
		return "", 0, 0, errors.Wrap(ctx, err, "read filter choice")
	}

	choice := strings.TrimSpace(choiceStr)

	if choice == "2" {
		// Ask for hours count
		fmt.Print("\nHow many hours do you want before and after the timestamp? ")
		_, err = fmt.Scanf("%d", &hoursCount)
		if err != nil {
			// Try reading as string and convert
			hoursStr, err := reader.ReadString('\n')
			if err != nil {
				return "", 0, 0, errors.Wrap(ctx, err, "read hours count")
			}

			trimmed := strings.TrimSpace(hoursStr)
			if trimmed == "" {
				// Empty input - use default
				hoursCount = 0 // Will be set to default below
			} else {
				_, err = fmt.Sscanf(trimmed, "%d", &hoursCount)
				if err != nil {
					// Not returning error, just use default value
					hoursCount = 0 // Will be set to default below
				}
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
		_, err = fmt.Scanf("%d", &lineCount)
		if err != nil {
			// Try reading as string and convert
			lineStr, err := reader.ReadString('\n')
			if err != nil {
				return "", 0, 0, errors.Wrap(ctx, err, "read line count")
			}

			trimmed := strings.TrimSpace(lineStr)
			if trimmed == "" {
				// Empty input - use default
				lineCount = 0 // Will be set to default below
			} else {
				_, err = fmt.Sscanf(trimmed, "%d", &lineCount)
				if err != nil {
					// Not returning error, just use default value
					lineCount = 0 // Will be set to default below
				}
			}
		}

		// Default to 100 if no valid input
		if lineCount <= 0 {
			lineCount = 100
			fmt.Println("Using default line count: 100 lines before and after")
		}
	}

	return normalizedTimestamp, lineCount, hoursCount, nil
}

// RunSurvey runs an interactive survey to collect user input
func RunSurvey(ctx context.Context, client *scalingo.ScalingoClient) (string, string, int, int, string, string, error) {
	var appName string
	var timestampInput string
	var lineCount int
	var hoursCount int
	var env string
	var region string

	reader := bufio.NewReader(os.Stdin)

	// Ask for app name
	fmt.Print("What's the app name? ")
	appName, err := reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read app name")
	}
	appName = strings.TrimSpace(appName)

	// Ask for environment
	fmt.Println("\nWhich environment do you want to use?")
	fmt.Println("  1. Production (default)")
	fmt.Println("  2. Staging")
	fmt.Println("  3. Development")
	fmt.Print("Enter your choice (1-3): ")
	envChoice, err := reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read environment choice")
	}

	// Set environment based on choice
	switch strings.TrimSpace(envChoice) {
	case "2":
		env = scalingo.EnvStaging
	case "3":
		env = scalingo.EnvDev
	default:
		env = scalingo.EnvProduction
	}

	// Skip region selection for dev environment
	if env != scalingo.EnvDev {
		// Fetch available regions for the environment
		regions, err := client.GetRegions(ctx)
		if err != nil {
			fmt.Printf("Warning: Could not fetch regions: %v\n", err)
			fmt.Printf("Using default region: %s\n", scalingo.RegionOscFr1)
			region = scalingo.RegionOscFr1
		} else {
			fmt.Println("\nPlease select a region:")

			// Create a map of option number to region
			regionOptions := make(map[int]string)
			for i, r := range regions {
				fmt.Printf("  %d. %s (%s)\n", i+1, r.DisplayName, r.Name)
				regionOptions[i+1] = r.Name
			}

			// If no regions were found, use default
			if len(regions) == 0 {
				fmt.Printf("No regions found. Using default region: %s\n", scalingo.RegionOscFr1)
				region = scalingo.RegionOscFr1
			} else {
				// Ask for region selection
				fmt.Printf("Enter your choice (1-%d): ", len(regions))
				regionChoice, err := reader.ReadString('\n')
				if err != nil {
					return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read region choice")
				}

				regionChoiceInt := 0
				fmt.Sscanf(strings.TrimSpace(regionChoice), "%d", &regionChoiceInt)

				// If invalid choice, use first region
				if regionChoiceInt < 1 || regionChoiceInt > len(regions) {
					fmt.Println("Invalid choice. Using first region.")
					region = regions[0].Name
				} else {
					region = regionOptions[regionChoiceInt]
				}
			}
		}
	}

	// Check if the app exists in the selected environment and region
	err = client.CheckAppExists(ctx, appName)
	if err != nil {
		fmt.Printf("\nError: The app '%s' does not exist in the %s environment (%s region).\n", appName, env, region)
		return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "find app")
	}

	// Ask for timestamp
	fmt.Println("\nAround what time should we search? Please use one of the following formats:")
	fmt.Println("  - YYYY-MM-DD HH:MM:SS (e.g., 2023-06-15 14:30:00)")
	fmt.Println("  - Today at HH:MM:SS (e.g., Today at 14:30:00)")
	fmt.Println("  - Yesterday at HH:MM:SS (e.g., Yesterday at 14:30:00)")
	fmt.Println("  - Monday/Tuesday/etc. at HH:MM:SS (e.g., Monday at 14:30:00)")
	fmt.Print("Timestamp: ")
	timestampInput, err = reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read timestamp")
	}

	// Validate and normalize the timestamp
	normalizedTimestamp, err := timestamp.ValidateAndNormalize(ctx, strings.TrimSpace(timestampInput))
	if err != nil {
		return "", "", 0, 0, "", "", err
	}

	// Ask how they want to filter - by lines or hours
	fmt.Println("\nDo you want to filter by lines or hours around the timestamp?")
	fmt.Println("  1. Lines (default)")
	fmt.Println("  2. Hours")
	fmt.Print("Enter your choice (1 or 2): ")
	choiceStr, err := reader.ReadString('\n')
	if err != nil {
		return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read filter choice")
	}

	choice := strings.TrimSpace(choiceStr)

	if choice == "2" {
		// Ask for hours count
		fmt.Print("\nHow many hours do you want before and after the timestamp? ")
		_, err = fmt.Scanf("%d", &hoursCount)
		if err != nil {
			// Try reading as string and convert
			hoursStr, err := reader.ReadString('\n')
			if err != nil {
				return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read hours count")
			}

			trimmed := strings.TrimSpace(hoursStr)
			if trimmed == "" {
				// Empty input - use default
				hoursCount = 0 // Will be set to default below
			} else {
				_, err = fmt.Sscanf(trimmed, "%d", &hoursCount)
				if err != nil {
					// Not returning error, just use default value
					hoursCount = 0 // Will be set to default below
				}
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
		_, err = fmt.Scanf("%d", &lineCount)
		if err != nil {
			// Try reading as string and convert
			lineStr, err := reader.ReadString('\n')
			if err != nil {
				return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read line count")
			}

			trimmed := strings.TrimSpace(lineStr)
			if trimmed == "" {
				// Empty input - use default
				lineCount = 0 // Will be set to default below
			} else {
				_, err = fmt.Sscanf(trimmed, "%d", &lineCount)
				if err != nil {
					// Not returning error, just use default value
					lineCount = 0 // Will be set to default below
				}
			}
		}

		// Default to 100 if no valid input
		if lineCount <= 0 {
			lineCount = 100
			fmt.Println("Using default line count: 100 lines before and after")
		}
	}

	return appName, normalizedTimestamp, lineCount, hoursCount, env, region, nil
}
