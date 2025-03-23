package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/config"
	"github.com/briceamen/scalilogs/internal/timestamp"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

// RunSurveyFirstPart runs the first part of the interactive survey without needing a client
// It only collects app name, environment and region
func RunSurveyFirstPart(ctx context.Context, regions map[string][]config.Region) (string, string, string, error) {
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

	trimmedChoice := strings.TrimSpace(envChoice)
	// Set environment based on choice
	switch trimmedChoice {
	case "", "1": // Empty input or "1" both select the default option
		env = scalingo.EnvProduction
	case "2":
		env = scalingo.EnvStaging
	case "3":
		env = scalingo.EnvDev
	default:
		env = scalingo.EnvProduction
	}

	// Based on the environment, use the possible regions in the app config (production have two regions)
	region = ""
	for _, r := range regions[env] {
		if r.Default {
			region = r.Name
			break
		}
	}

	// If we're in production environment, we should offer a choice between the two regions
	if env == scalingo.EnvProduction && len(regions[env]) > 1 {
		fmt.Println("\nPlease select a region:")
		for i, r := range regions[env] {
			defaultText := ""
			if r.Default {
				defaultText = " (default)"
			}
			fmt.Printf("  %d. %s (%s)%s\n", i+1, r.DisplayName, r.Name, defaultText)
		}

		fmt.Printf("Enter your choice (1-%d): ", len(regions[env]))
		regionChoice, err := reader.ReadString('\n')
		if err != nil {
			return "", "", "", errors.Wrap(ctx, err, "read region choice")
		}

		trimmedRegionChoice := strings.TrimSpace(regionChoice)
		regionChoiceInt := 0

		// Empty input selects default region
		if trimmedRegionChoice == "" {
			// Keep using default region already set above
		} else {
			fmt.Sscanf(trimmedRegionChoice, "%d", &regionChoiceInt)
			// If valid choice, use selected region
			if regionChoiceInt >= 1 && regionChoiceInt <= len(regions[env]) {
				region = regions[env][regionChoiceInt-1].Name
			}
		}
	} else if region != "" {
		fmt.Printf("Using region: %s\n", region)
	}

	return appName, env, region, nil
}

// RunSurveySecondPart runs the second part of the interactive survey after a client has been created
// It verifies the app exists and collects timestamp and filter preferences
func RunSurveySecondPart(ctx context.Context, client *scalingo.Client, appName string, env string, region string) (string, int, int, error) {
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
	fmt.Println("  • Absolute date: 2023-06-15 14:30:00")
	fmt.Println("  • With 'at': 2025-03-22 at 12:00 or 2025-03-22 at 12")
	fmt.Println("  • Relative day: Today at 14:30 or Yesterday at 14:30")
	fmt.Println("  • Weekday: Monday at 14:30")
	fmt.Println("  • Current time: now")
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
	// Empty input selects option 1 (Lines)
	if choice == "" || choice == "1" {
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
	} else if choice == "2" {
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
	}

	return normalizedTimestamp, lineCount, hoursCount, nil
}

// RunSurvey runs an interactive survey to collect user input
func RunSurvey(ctx context.Context, client *scalingo.Client) (string, string, int, int, string, string, error) {
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

	trimmedChoice := strings.TrimSpace(envChoice)
	// Set environment based on choice
	switch trimmedChoice {
	case "", "1": // Empty input or "1" both select the default option
		env = scalingo.EnvProduction
	case "2":
		env = scalingo.EnvStaging
	case "3":
		env = scalingo.EnvDev
	default:
		env = scalingo.EnvProduction
	}

	// Skip region selection for dev environment
	if env != scalingo.EnvDev {
		// Try to get regions from the client first
		regions, err := client.GetRegions(ctx)
		if err != nil {
			// If client.GetRegions fails, try from cache
			fmt.Printf("Warning: Could not fetch regions from API: %v\n", err)
			fmt.Println("Attempting to load regions from cache...")

			regions, err = config.LoadRegionsFromCache(ctx, env, "")
			if err != nil {
				fmt.Printf("Warning: Could not load regions from cache: %v\n", err)
			}
		}

		if len(regions) == 0 {
			return "", "", 0, 0, "", "", errors.New(ctx, "no regions found for environment")
		} else if len(regions) == 1 {
			// Automatically select the only available region
			region = regions[0].Name
			fmt.Printf("Only one region available: %s (%s). Automatically selected.\n", regions[0].DisplayName, regions[0].Name)
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
				return "", "", 0, 0, "", "", errors.Wrap(ctx, err, "read region choice")
			}

			trimmedRegionChoice := strings.TrimSpace(regionChoice)
			regionChoiceInt := 0

			// Empty input selects the first region
			if trimmedRegionChoice == "" {
				region = regions[0].Name
			} else {
				fmt.Sscanf(trimmedRegionChoice, "%d", &regionChoiceInt)
				// If valid choice, use selected region
				if regionChoiceInt >= 1 && regionChoiceInt <= len(regions) {
					region = regionOptions[regionChoiceInt]
				} else {
					fmt.Println("Invalid choice. Using first region.")
					region = regions[0].Name
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
	fmt.Println("  • Absolute date: 2023-06-15 14:30:00")
	fmt.Println("  • With 'at': 2025-03-22 at 12:00 or 2025-03-22 at 12")
	fmt.Println("  • Relative day: Today at 14:30 or Yesterday at 14:30")
	fmt.Println("  • Weekday: Monday at 14:30")
	fmt.Println("  • Current time: now")
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
	// Empty input selects option 1 (Lines)
	if choice == "" || choice == "1" {
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
	} else if choice == "2" {
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
	}

	return appName, normalizedTimestamp, lineCount, hoursCount, env, region, nil
}
