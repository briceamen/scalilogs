package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/briceamen/scalilogs/internal/logs"
	"github.com/briceamen/scalilogs/internal/tui"
	"github.com/briceamen/scalilogs/internal/ui"
	"github.com/briceamen/scalilogs/pkg/scalingo"
)

func main() {
	// Create a root context for the entire application
	ctx := context.Background()
	statusCh := make(chan tui.StatusMessage)

	// Define command-line flags
	var appNameFlag string
	var timestampFlag string
	var lineCountFlag int
	var hoursFlag int
	var envFlag string
	var regionFlag string

	// Short flags
	flag.StringVar(&appNameFlag, "a", "", "App name")
	flag.StringVar(&timestampFlag, "t", "", "Timestamp (format: YYYY-MM-DD HH:MM:SS, 'now', or 'today at HH:MM:SS')")
	flag.IntVar(&lineCountFlag, "l", 0, "Number of lines before and after timestamp")
	flag.IntVar(&hoursFlag, "h", 0, "Number of hours before and after timestamp")
	flag.StringVar(&envFlag, "e", scalingo.EnvProduction, "Environment (production/staging/dev)")
	flag.StringVar(&regionFlag, "r", "", "Region (e.g., osc-fr1, osc-secnum-fr1)")

	// Long flags
	flag.StringVar(&appNameFlag, "app", "", "App name")
	flag.StringVar(&timestampFlag, "timestamp", "", "Timestamp (format: YYYY-MM-DD HH:MM:SS, 'now', or 'today at HH:MM:SS')")
	flag.IntVar(&lineCountFlag, "lines", 0, "Number of lines before and after timestamp")
	flag.IntVar(&hoursFlag, "hours", 0, "Number of hours before and after timestamp")
	flag.StringVar(&envFlag, "env", scalingo.EnvProduction, "Environment (production/staging/dev)")
	flag.StringVar(&regionFlag, "region", "", "Region (e.g., osc-fr1, osc-secnum-fr1)")

	// Custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -a, --app string       App name\n")
		fmt.Fprintf(os.Stderr, "  -t, --timestamp string Timestamp (format: YYYY-MM-DD HH:MM:SS, 'now', or 'today at HH:MM:SS')\n")
		fmt.Fprintf(os.Stderr, "  -l, --lines int        Number of lines before and after timestamp\n")
		fmt.Fprintf(os.Stderr, "  -h, --hours int        Number of hours before and after timestamp\n")
		fmt.Fprintf(os.Stderr, "  -e, --env string       Environment (production/staging/dev, default: production)\n")
		fmt.Fprintf(os.Stderr, "  -r, --region string    Region (e.g., osc-fr1, osc-secnum-fr1)\n\n")
		fmt.Fprintf(os.Stderr, "If flags are not provided, interactive mode will start.\n")
	}

	flag.Parse()

	// Validate environment parameter
	if envFlag != scalingo.EnvProduction && envFlag != scalingo.EnvStaging && envFlag != scalingo.EnvDev {
		fmt.Fprintf(os.Stderr, "Error: Invalid environment '%s'. Must be one of: production, staging, dev\n", envFlag)
		flag.Usage()
		os.Exit(1)
	}

	// Determine if we're using command-line flags or interactive mode
	useFlags := appNameFlag != "" || timestampFlag != "" || lineCountFlag > 0 || hoursFlag > 0

	var appName string
	var timestamp string
	var lineCount int
	var hours int
	var env string
	var region string
	var err error
	var client *scalingo.ScalingoClient

	if useFlags {
		// Use command-line flags
		appName = appNameFlag
		timestamp = timestampFlag
		lineCount = lineCountFlag
		hours = hoursFlag
		env = envFlag
		region = regionFlag

		// Validate required parameters
		if appName == "" {
			fmt.Fprintf(os.Stderr, "Error: App name is required\n")
			flag.Usage()
			os.Exit(1)
		}

		// Default to 100 lines if not specified and hours not specified
		if lineCount <= 0 && hours <= 0 {
			lineCount = 100
		}

		// Create the client with specified parameters
		client, err = scalingo.NewScalingoClient(ctx, env, region, statusCh)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing Scalingo client: %v\n", err)
			os.Exit(1)
		}

		// Verify the app exists using the client
		err = client.CheckAppExists(ctx, appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: The app '%s' does not exist in the %s environment", appName, env)
			if region != "" {
				fmt.Fprintf(os.Stderr, " (%s region)", region)
			}
			fmt.Fprintln(os.Stderr, ".")
			os.Exit(1)
		}
	} else {
		// Interactive mode with survey
		// First run the survey without a client to get environment/region
		appName, env, region, err = ui.RunSurveyFirstPart(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Now create the client with the selected environment and region
		client, err = scalingo.NewScalingoClient(ctx, env, region, statusCh)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing Scalingo client: %v\n", err)
			os.Exit(1)
		}

		// Run the second part of the survey with the initialized client
		timestamp, lineCount, hours, err = ui.RunSurveySecondPart(ctx, client, appName, env, region)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Use the client to extract logs
	outputFilePath, err := logs.ExtractLogs(ctx, client, appName, timestamp, lineCount, hours)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print success message with file path
	fmt.Println("✓ Extraction complete!")
	fmt.Printf("Logs saved to: %s\n", outputFilePath)
}
